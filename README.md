*navc* is a daemon to index and navigate your C code. It watches for all file
changes and automatically update the index. It provides a RPC API to ask for
definition, declaration, calls, and uses of some symbol. This API can be used
by any editor plugin to point to the correct location of the looked up symbol.
navc uses clang to parse the file. Having the abstract syntax tree of the code
can be very powerful as it can know with greater exactitude the location of the
declaration or definition being looked up. The project is limited to C for now,
but it can be extended to C++ as clang it is able to parse it.

(Wish) List of Query Capabilities
=================================
* Uses of a symbol
* Definition of a function
* (All) Declaration of a symbol: functions, variables, structs, typedef, enums,
  defines
* Implementations of a function pointer (is this possible?)
* Assignments of a variable

Installation
============
You need to have the development headers for clang. In Ubuntu this is the
package ``libclang_dev``. Once this is installed, you need to simply run:

```
	CGO_CFLAGS="-I`llvm-config --includedir`" \
	CGO_LDFLAGS="-L`llvm-config --libdir`" \
	go get github.com/google/navc
```

The binary will be located in $GOPATH/bin/navc.

For the vim plugin to work, you need vim with python support. I added an
installer to make the use of the plugin easier. It assumes that your vim uses
``~/.vim`` as config directory. You can install the plugin by running:

```
	$ cd $GOPATH/src/github.com/google/navc/editor/vim/
	$ ./install.sh
```

To uninstall the plugin, just run ``./install.sh -u``.

TODO
====
* Add navigation for structs, typedef, enums, and defines.
* Have better logging and not log everything. In particular, it would be nice
  to have a progress bar while indexing code at start up.
* Currently, to update the compile\_commands.json file in memory, the daemon
  has to be restarted. This can be fixed easily by simply watching all the
  compile\_commands.json files and update the database in memory if it changed.

DISCLAIMER
==========
This is not a official Google project and it is not supported by Google Inc.
