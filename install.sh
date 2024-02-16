#!/bin/bash

# Compile the Go code
make build

# Move the binary to a directory in PATH
install_dir="/usr/local/bin"
install_path="$install_dir/artemis"

if [ -e "$install_path" ]; then
    echo "Error: File already exists at $install_path"
    exit 1
fi

sudo mv artemis "$install_dir"

# Set appropriate permissions
sudo chmod +x "$install_path"

echo "Artemis has been installed successfully to $install_path"
