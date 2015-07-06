*navc* is a daemon to index and navigate your C code. It watches for all file
changes and automatically update the index. It provides a RPC API to ask for
definition, declaration, calls, and uses of some symbol. This API can be used
by any editor plugin to point to the correct location of the looked up symbol.
navc uses clang to parse the file. Having the abstract syntax tree of the code
can be very powerful as it can know with greater exactitude the location of the
declaration or definition being looked up. The project is limited to C for now,
but it can be extended to C++ as clang it is able to parse it.

navc is in its early stages. There is still plenty of code to be written.

(Wish) List of Query Capabilities
=================================
* Uses of a symbol
* Definition of a function
* (All) Declaration of a symbol: functions, variables, typedef, enums, defines
* Implementations of a function pointer (is this possible?)
* Assignments of a variable

Requirements
============
For the daemon you need the following libraries:
* libclang: https://github.com/sbinet/go-clang
* fsnotify: https://github.com/go-fsnotify/fsnotify
* sqlite3: https://github.com/mattn/go-sqlite3

For the vim plugin to work, you need vim with python support. You also need to
include the jsonrpc file in the PYTHONPATH env variable. Examples can be found
in navc.vim.

TODO
====
* Fix backup of in-memory database. Currently, the connection for the backup
  can be gone when performing the backup. We need to create a fresh connection
  each time we have a backup.
* Do we really need to periodically update the DB? Maybe backing up at daemon
  shutdown is enough.
* We need to introduce the dependency between files so that all the dependents
  get updated as soon as a file changes.
* We are not including the right header directories missing a lot of the symbol
  declarations. We can use the JSON compilation database in clang
  (http://clang.llvm.org/docs/JSONCompilationDatabase.html). This can be
  generated with CMake if used or bear otherwise.
* Find a way to parse files in parallel either by concurrently writing to the
  DB or have a map/reduce kind of format where map=parse, reduce=insert in DB.
* Complete vim plugin for use.
* Have better logging and not log everything. In particular, it would be nice
  to have a progress bar while indexing code at start up.

DISCLAIMER
==========
This is not a official Google project and it is not supported by Google Inc.