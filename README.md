# ArSamba (Subservice)

The Samba subservice for arozos system. 

**THIS IS A SUBSERVICE. Do not install using the Module Installer**



## Installation	

Requirement

- Go 1.15 or above
- Debian Buster or above
- Samba



```bash
# Install Samba
sudo apt-get update -y
sudo apt-get install samba -y

# Assuming your arozos root is located at ~/arozos/
cd ~/arozos/subservice/

# Pull toe ArSamba into the subservice directory
git clone https://github.com/aroz-online/ArSamba

# Build the ArSamba
cd ./ArSamba
./build.sh

# Set the correct permission for the files (Optional)
sudo chmod 755 -R ./

```



## License

MIT License

See the license file for more information

