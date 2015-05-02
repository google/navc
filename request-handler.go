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

type RequestHandler struct {
    db  *SymbolsDB
}

func (rh *RequestHandler) GetFuncDef(fun string, file *string) error {
    // TODO: return function definitions with the matching name.
    // Note that the output should be a list of structures with
    // file names and locations within the file

    /*
    funs, _ := db.GetFunctions("main")
    for _, f := range funs {
        log.Println(f)
    }
    */

    return nil
}

func (rh *RequestHandler) GetSymbolDecl(symbol string, file *string) error {
    // TODO: return symbol declaration with the matching name.
    // Same note as previous function (return is a placeholder)
    return nil
}
