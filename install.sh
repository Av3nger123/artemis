#!/bin/bash

# Compile the Go code: every database driver, so a db step against SQL Server,
# Oracle, MongoDB or Redis works out of the box. `make build` is the smaller
# binary with PostgreSQL, MySQL and SQLite only.
make build-full

# Move the binary to a directory in PATH
install_dir="/usr/local/bin"
install_path="$install_dir/artemis"

if [ -e "$install_path" ]; then
    echo "Error: File already exists at $install_path"
fi

sudo mv artemis "$install_dir"

# Set appropriate permissions
sudo chmod +x "$install_path"

echo "Artemis has been installed successfully to $install_path"
