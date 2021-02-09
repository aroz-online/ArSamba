package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"git.arozos.com/ArSamba/apt"

	"git.arozos.com/ArSamba/aroz"
)

var (
	handler *aroz.ArozHandler
)

func SetupCloseHandler() {
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("\r- Shutting down ArSamba module.")

		os.Exit(0)
	}()
}

func main() {
	//If you have other flags, please add them here

	//Start the aoModule pipeline (which will parse the flags as well). Pass in the module launch information
	handler = aroz.HandleFlagParse(aroz.ServiceInfo{
		Name:        "ArSamba",
		Desc:        "arozos Samba Setting Subservice",
		Group:       "System Settings",
		IconPath:    "arsamba/img/icon.png",
		Version:     "1.0",
		StartDir:    "arsamba/index.html",
		SupportFW:   true,
		LaunchFWDir: "arsamba/index.html",
		InitFWSize:  []int{350, 560},
	})

	//Register the standard web services urls
	fs := http.FileServer(http.Dir("./web"))
	http.HandleFunc("/create", handleNewUser)
	http.HandleFunc("/remove", handleUserRemove)
	http.HandleFunc("/getStatus", handleGetStatus)
	http.Handle("/", fs)

	SetupCloseHandler()

	go func(port string) {
		log.Println("ArSamba subservice started. Listening on " + handler.Port)
		err := http.ListenAndServe(port, nil)
		if err != nil {
			log.Fatal(err)
		}
	}(handler.Port)

	//Mkdir for user samba profile
	os.MkdirAll("./profiles", 0755)

	//Install samba if it is not installed
	pm := apt.NewPackageManager(true)
	err := pm.InstallIfNotExists("samba", true)
	if err != nil {
		log.Println("Unable to install samba on this host! Assume already exists")
	}

	//Do a blocking loop
	select {}
}

func handleGetStatus(w http.ResponseWriter, r *http.Request) {
	//Get username from request
	username, _ := handler.GetUserInfoFromRequest(w, r)

	if runtime.GOOS == "windows" {
		sendErrorResponse(w, "not supported platform")
		return
	}
	//Check if the user has already in samba user
	log.Println("Checking User Status", username)
	userExists := false
	out, err := execute("pdbedit -L | grep " + username)
	if err != nil {
		userExists = false
	}

	if strings.TrimSpace(string(out)) != "" {
		userExists = true
	}

	//Send the results
	js, _ := json.Marshal(userExists)
	sendJSONResponse(w, string(js))
}

func handleNewUser(w http.ResponseWriter, r *http.Request) {
	//Get the required information
	username, err := mv(r, "username", true)
	if err != nil {
		sendErrorResponse(w, "Invalid username given")
		return
	}

	//Match the session username
	proxyUser, token := handler.GetUserInfoFromRequest(w, r)
	if username != proxyUser {
		sendErrorResponse(w, "User not logged in")
		return
	}

	password, err := mv(r, "password", true)
	if err != nil {
		sendErrorResponse(w, "Invalid password given")
		return
	}

	//Add the user to samba
	log.Println("Adding User", username)
	//Add user to linux
	out, _ := execute("useradd -m \"" + username + "\"")
	log.Println(string(out))

	//Set password for the new user
	out, _ = execute(`(echo "` + password + `"; sleep 1; echo "` + password + `";) | passwd "` + username + `"`)
	log.Println(string(out))

	//Add it to samba user
	out, _ = execute(`(echo "` + password + `"; sleep 1; echo "` + password + `" ) | sudo smbpasswd -s -a "` + username + `"`)
	log.Println(string(out))

	//Create an AGI Call that get the user's storage directories files
	script := `
	requirelib("filelib");
	//Get the roots of this user
	var roots = filelib.glob("/");
	var userdirs = [];
	for (var i = 0; i < roots.length; i++){
		//Translate all these roots to realpath
		userdirs.push([roots[i].split(":").shift(), decodeAbsoluteVirtualPath(roots[i]), pathCanWrite(roots[i])])
	}
	
	sendJSONResp(JSON.stringify(userdirs))
	`

	userProfile := []string{}

	//Execute the AGI request on server side
	resp, err := handler.RequestGatewayInterface(token, script)
	if err != nil {
		//Something went wrong when performing POST request
		log.Println(err)
	} else {
		//Try to read the resp body
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Println(err)
			w.Write([]byte(err.Error()))
			return
		}
		resp.Body.Close()

		log.Println(string(bodyBytes))

		//Decode the json
		type Results [][]interface{}
		results := new(Results)
		err = json.Unmarshal(bodyBytes, &results)
		if err != nil {
			log.Println(err)
			return
		}

		log.Println(results)

		//Generate user root folders
		for _, r := range *results {
			if len(r) == 3 {
				pathname := r[0].(string)
				if pathname == "tmp" {
					//Do not expose tmp folder
					continue
				}
				rpath := r[1].(string)
				canWrite := r[2].(bool)
				uuidOfStorage := username + " (" + pathname + ")"
				if canWrite {
					userProfile = append(userProfile, `[`+uuidOfStorage+`]
	comment=`+username+"'s "+pathname+`
	path=`+rpath+`
	read only=no
	valid users = `+username+`
	guest ok=no
	browseable=yes
	create mask=0777
	directory mask=0777`)
				} else {
					userProfile = append(userProfile, `[`+uuidOfStorage+`]
	comment=`+username+"'s "+pathname+`
	path=`+rpath+`
	read only=yes
	valid users = `+username+`
	guest ok=no
	browseable=yes
	create mask=0777
	directory mask=0777`)
				}
			}
		}

	}

	log.Println(strings.Join(userProfile, "\n\n"))

	//Write the user profiles to file
	ioutil.WriteFile("./profiles/"+username+".conf", []byte(strings.Join(userProfile, "\n\n")), 0755)

	updateSmbConfig()
	//Return ok
	sendOK(w)
}

func handleUserRemove(w http.ResponseWriter, r *http.Request) {
	//Get the required information
	username, err := mv(r, "username", true)
	if err != nil {
		sendErrorResponse(w, "Invalid username given")
		return
	}

	//Match the session username
	proxyUser, _ := handler.GetUserInfoFromRequest(w, r)
	if username != proxyUser {
		sendErrorResponse(w, "User not logged in")
		return
	}

	//OK! Remove user
	log.Println("Remove user", username)

	//Remove user from samba
	out, _ := execute("smbpasswd -x \"" + username + "\"")
	log.Println(string(out))

	//Remove user from linux as well
	out, _ = execute("userdel -r  \"" + username + "\"")
	log.Println(string(out))

	//Remove user profiles
	if fileExists("./profiles/" + username + ".conf") {
		os.Remove("./profiles/" + username + ".conf")
	}
	updateSmbConfig()

	//Return OK
	sendOK(w)
}

func updateSmbConfig() {
	//Update the system config
	profiles, _ := filepath.Glob("./profiles/*.conf")
	base, _ := ioutil.ReadFile("smb.conf")
	additionalProfiles := []string{}
	for _, profile := range profiles {
		thisProfileContent, _ := ioutil.ReadFile(profile)
		additionalProfiles = append(additionalProfiles, string(thisProfileContent))
	}

	finalConfigFile := string(base) + strings.Join(additionalProfiles, "\n\n")

	ioutil.WriteFile("/etc/samba/smb.conf", []byte(finalConfigFile), 0777)

	out, err := execute("systemctl restart smbd.service")
	log.Println("Samba restarted: ", string(out), err)
}

func execute(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}
