invenv
==============

`invenv` is a tool to automatically create and run your Python scripts
in a virtual environment with installed dependencies.

It tries to simplify running Python scripts (or even applications!) by taking
from you the burden of creating and maintaining virtual environments.

### Description
```
Usage:
  invenv [invenv-flags] -- [VAR=val] python-script.py [flags]

Examples:
invenv -- somepath/myscript.py
invenv -n -- somepath/myscript.py --version
invenv -r req.txt -- DEBUG=1 somepath/myscript.py

Flags:
  -d, --debug                      enable debug mode with verbose output
  -h, --help                       help for invenv
  -n, --new-environment            create a new virtual environment even if it already exists
  -p, --python string              use specified Python interpreter
  -r, --requirements-file string   use specified requirements file. If not provided, it
                                   will try to guess the requirements file name:
                                   requirements_<script_name>.txt, <script_name>_requirements.txt or
                                   requirements.txt
  -v, --version                    print version and exit
  -w, --which                      print the location of virtual environment folder and exit. If
                                   the virtual environment does not exist, it will be created with
                                   installed requirements
```

### Details
When you run `invenv` the first time it will:
 - detect python interpreter which should be used to run your script (by analyzing shebang)
   - in case if python interpreter is not found in your `PATH`, it will try to use default python interpreter in your system
   - it is possible to specify a custom interpreter with `-p` flag
 - create a virtual environment in `~/.local/invenv/` folder
 - try to automatically install all dependencies from `requirements_<script_name>.txt` or
   `requirements.txt` files (it is possible to specify a custom requirements file with `-r` flag)
 - run your script with all the arguments you passed

Next time you run `invenv` it will try to use the existing virtual environment and install
dependencies only if they are changed.

### Installation
 - Using [grm](https://github.com/jsnjack/grm)
    ```bash
    grm install jsnjack/invenv
    ```
 - Download binary from [Release](https://github.com/jsnjack/invenv/releases/latest/) page
 - One liner:
   ```bash
   curl -s https://api.github.com/repos/jsnjack/invenv/releases/latest | jq -r .assets[0].browser_download_url | xargs curl -LOs && chmod +x invenv && sudo mv invenv /usr/local/bin/
   ```

### Credits
- [qguv](https://github.com/qguv) for the original idea
