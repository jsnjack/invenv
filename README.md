ave
==============

`ave` - automatic virtual environment. It's a tool to automatically create and
activate a virtual environment for your Python script.

### Description
```
Usage:
  ave [-d] [-r string] [-n] -- python-script.py arg1 arg2 [flags]

Flags:
  -d, --debug                      enable debug mode with verbose output
  -h, --help                       help for ave
  -n, --new-environment            create a new virtual environment even if it already exists
  -r, --requirements-file string   use specified requirements file
```

When you run `ave` the first time it will:
 - create a virtual environment in `~/.local/ave/` folder
 - try to automatically install all dependencies from `requirements_<script_name>.txt` or
   `requirements.txt` files (it is possible to specify a custom requirements file with `-r` flag)
 - run your script with all the arguments you passed

Next time you run `ave` it will try to use the existing virtual environment and install
dependencies only if they are changed.

### Installation
 - Using [grm](https://github.com/jsnjack/grm)
    ```bash
    grm install jsnjack/ave
    ```
 - Download binary from [Release](https://github.com/jsnjack/ave/releases/latest/) page
