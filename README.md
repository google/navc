*navc* is a daemon to index and navigate your C code. It watches for all file
changes and automatically update the index. It provides a RPC API to ask for
definition, declaration, calls, and uses of some symbol. This API can be used
by any editor plugin to point to the correct location of the looked up symbol.
navc uses clang to parse the file. Having the abstract syntax tree of the code
can be very powerful as it can know with greater exactitude the location of the
declaration or definition being looked up. The project is limited to C only.

List of Query Capabilities
==========================
* Uses of a symbol
* Definition of a function
* All declarations of a symbol: functions, variables, structs, typedef, enums, defines.

Installation
============
You need to have the development headers for clang. In Ubuntu this is the
package ``libclang_dev``. Once this is installed, you need to simply run:

```
	CGO_CFLAGS="-I`llvm-config --includedir`" \
	CGO_LDFLAGS="-L`llvm-config --libdir`" \
	go get github.com/google/navc
```

The binary will be located in $GOPATH/bin/navc. Make sure to have $GOPATH/bin in
your PATH.

VIM plugin
----------

The vim plugin is very basic but usable. For the vim plugin to work, you need
vim with python support. I added an installer to make the use of the plugin
easier. It assumes that your vim uses ``~/.vim`` as config directory. You can
install the plugin by running:

```
	$ cd $GOPATH/src/github.com/google/navc/editor/vim/
	$ ./install.sh
```

To uninstall the plugin, just run ``./install.sh -u``.

Using *navc*
============

You should simply *cd* into your project directory and start the daemon:

```
	$ cd $HOME/my/project/
	$ navc
```

If you have a non-standard set of compilation flags (usual on large projects),
you probably want to use clang's
[compile_commands.json](http://clang.llvm.org/docs/JSONCompilationDatabase.html)
database. This can be generated with [bear](https://github.com/rizsotto/Bear) or
with cmake if available. Assuming that a project is compiled with make (e.g. Linux kernel) you simply need to run:
```
	$ bear make
```

Once *navc* index your project, from vim you simply place the cursor on top of
the symbol to query and issue one of the following commands:

| Shortcut | Action                |
|----------|-----------------------|
| C-z d    | Go to definition      |
| C-z e    | Go to declaration     |
| C-z u    | List uses             |
| C-z b    | Go to previous symbol |

Caveats
=======

1. Since go version 1.6, the go clang library is having issues with pointers. To bypass this problem for now, navc has to be run disabling the pointer checks:
```
	$ GODEBUG=cgocheck=0 navc
```
1. Currently, to update the compile\_commands.json file in memory, the daemon
has to be restarted.


TODO
====
* Have better logging and not log everything. In particular, it would be nice
to have a progress bar while indexing code at start up.
* Watch all the compile\_commands.json files and update the database in memory
with any change.

DISCLAIMER
==========
This is not a official Google project and it is not supported by Google Inc.
