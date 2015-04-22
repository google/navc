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

func Parse(file string, db *SymbolsDB) {
    // insert file in DB first
    db.InsertFile(file)

    // insert symbols
    idx := clang.NewIndex(0, 1)
    defer idx.Dispose()

    args := []string{}

    tu := idx.Parse(file, args, nil, 0)
    defer tu.Dispose()

    visitNode := func (cursor, parent clang.Cursor) clang.ChildVisitResult {
        if cursor.IsNull() {
            return clang.CVR_Continue
        }

        fmt.Printf("%s: %s (%s)\n",
            cursor.Kind().Spelling(), cursor.Spelling(), cursor.USR())
        fName, line, col, _ := cursor.Location().GetFileLocation()
        fmt.Println(fName.Name(), ":", line, col)

        switch cursor.Kind() {
        case clang.CK_FunctionDecl:
            fun := &Function{cursor.Spelling(), fName.Name(), int(line), int(col)}
            db.InsertFunction(fun)
            //TODO: recurse when looking for variables and funtion calls
            return clang.CVR_Continue
        }
        return clang.CVR_Continue
    }

    tu.ToCursor().Visit(visitNode)
}
