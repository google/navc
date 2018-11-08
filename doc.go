/*
 * Copyright 2015 Google Inc. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/*
 * TODO: Explain here the design. I think I already did somewhere.
 *
 * Symbols Database
 *
 * TODO: NEEDS UPDATE WITH THE NEW DATABASE!
 * The symbols database keeps all the index of the code. We keep one file (or
 * database file) per indexed C file. This file will contain the information of
 * the code in the C file and all the headers included recursively (the headers
 * included in the headers). We call this translation unit or TU (following
 * clang's nomenclature). Having a database per translation unit will allow
 * greater parallelism and higher performance at indexing time. All these files
 * are stored in the symbols directory, .navc_dbsymbols by default, and
 * represented by the struct symbolsDB. In the symbols directory, each database
 * file uses the sha1 of the original file name as file name. The file will
 * simply have a serialized form of some of the fields in the structure
 * symbolsTUDB. This structure has the following fields:
 *
 * - File: Name of the source file indexed.
 *
 * - Mtime: Modification time of the file when was indexed.
 *
 * - Headers (fileID -> Time): Contains all the header files included in the
 * translation unit and the modification time of each when the translation unit
 * was indexed.
 *
 * - SymLoc (symbolLoc -> symbolID): Contains all the symbols uses in the
 * translatio unit. It maps symbol locations to symbol ID.
 *
 * - SymData (symbolID -> symbolData): Contains the data of all the symbols
 * indexed by symbol ID. Given any symbol location, we can find its symbol data
 * in the translation unit. The symbol data will have the list of declarations
 * of the symbol and the list of uses in the translation unit. If the definition
 * of the symbol is available in this translation unit, DefAvail will be true
 * and Def will hold the location of the definition.
 *
 * - Includers: In case the translation unit represent a header file, this list
 * will have all the translation units including this file. This is the only
 * information necessary for header files. Header files is where two translation
 * units meet. For instance, one function declared in a.h and used by a.c can be
 * defined in b.c. The meeting point of a.c and b.c is their included header
 * file a.h. Keeping this information is not really necessary but it speed up
 * lookup of symbols. In theory, these can be recreated from the regular
 * translation units.
 *
 * fileID and symbolID are simply a hash of the name of the file or symbol. In
 * this case, it is the sha1 hash of the names.
 *
 * symbolsDB has a map with an entry for every symbolsTUDB. On each entry, it
 * caches some information of the translation unit. Translations units are
 * inserted in the InsertTUDB function. This function will also be called to
 * replace an old translation unit of a file. Translation units will be
 * persisted to disk whenever the symbolsDB is flushed. This is done by calling
 * the FlushDB function.
 */
package main
