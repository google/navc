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
            call := getSymbolFromCursor(&cursor)
            decCursor := cursor.Referenced()
            dec := getSymbolFromCursor(&decCursor)
            db.InsertFuncCall(call, dec)
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
