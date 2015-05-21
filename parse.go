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
  * We can do something more interesting than simply look for symbols. Given that
  * we are parsing the whole file, we can create more tight dependencies between
  * symbols use and its declaration. We can create a symbol table where we keep
  * all the declarations of symbols and its location. Whenever we find a symbol
  * used, we look at the symbol table and insert the symbol use in the DB as well
  * as a foreign key to its declaration. We can do the same on function definitions.
  * This way, if the definition is available, we can know for sure the exact
  * function that is being called by joining the function definition and
  * the symbol use table. This is also true for variables.
  *
  * The symbol table can be implemented as a hash table of stacks. When we find
  * the declaration of a symbol, we insert the symbol in the hash. When its
  * scope is over, we remove it. Note that symbols with the same name will be
  * occasionally inserted in the table but this should work as long as the inner
  * symbol is first in the hash entry stack. To know what symbols to extract from
  * the symbol table, we have a separate stack where we put symbols as we find
  * them. Whenver a new block starts, we insert a special "start of block" symbol.
  * When the block ends, we can pop from the stack and the symbol table all the
  * symbols until we find the "start of block" especial symbol. This will ensure
  * that we pop all the symbols in the closing block.
  *
  * In terms of the DB, we need to create two more tables: (1) Symbols declaration,
  * (2) Symbols used. Function definitions table and symbols used table will have
  * a foreign key to symbols declaration. This should always be non-NULL as we
  * need a declaration in order to use the symbol (it wouldn't compile). Also
  * note that we may have more than one definition for a function in case there
  * are pre-processor conditions. We need to check for that.
  */

package main

import (
    "fmt"
    "github.com/sbinet/go-clang"
)

func getSymbolFromCursor(cursor *clang.Cursor) *Symbol {
    f, line, col, _ := cursor.Location().GetFileLocation()
    fName := f.Name()
    return &Symbol{cursor.Spelling(), fName, int(line), int(col)}
}

func Parse(file string, db *SymbolsDB) {
    // files already inserted in the DB from this parsing
    insertedFiles := map[string]bool{ file: true }

    // insert symbols
    idx := clang.NewIndex(0, 0)
    defer idx.Dispose()

    args := []string{}

    tu := idx.Parse(file, args, nil, clang.TU_DetailedPreprocessingRecord)
    defer tu.Dispose()

    visitNode := func (cursor, parent clang.Cursor) clang.ChildVisitResult {
        if cursor.IsNull() {
            return clang.CVR_Continue
        }

        f, line, col, _ := cursor.Location().GetFileLocation()
        fName := f.Name()

        if fName == "" {
            // ignore system code
            return clang.CVR_Continue
        }

        /* TODO: erase! this is not required */
        fmt.Printf("%s: %s (%s)\n",
            cursor.Kind().Spelling(), cursor.Spelling(), cursor.USR())
        fmt.Println(fName, ":", line, col)
        /*******************/

        if !insertedFiles[fName] {
            db.NeedToProcessFile(fName)
            insertedFiles[fName] = true
        }

        switch cursor.Kind() {
        case clang.CK_FunctionDecl:
            dec := getSymbolFromCursor(&cursor)

            if cursor.IsDefinition() {
                /* TODO: what if the definition is also the declaration? */
                db.InsertFuncDef(dec)
            } else {
                defCursor := cursor.DefinitionCursor()
                if !defCursor.IsNull() {
                    def := getSymbolFromCursor(&defCursor)
                    db.InsertFuncSymb(dec, def)
                } else {
                    db.InsertSymbol(dec)
                }
            }
        case clang.CK_VarDecl, clang.CK_ParmDecl:
            dec := getSymbolFromCursor(&cursor)
            db.InsertSymbol(dec)
        case clang.CK_CallExpr:
            //decCursor := cursor.Referenced()
            //dec := getSymbolFromCursor(&decCursor)
            //fmt.Println("declaration?", dec)
        case clang.CK_DeclRefExpr:
            use := getSymbolFromCursor(&cursor)
            decCursor := cursor.Referenced()
            dec := getSymbolFromCursor(&decCursor)
            db.InsertSymbolUse(use, dec)
        }

        /* TODO: eventually we need to continue on some cases for faster run */
        return clang.CVR_Recurse
    }

    tu.ToCursor().Visit(visitNode)
}
