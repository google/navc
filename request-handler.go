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

import "fmt"

type RequestHandler struct {
	db *SymbolsDB
}

func (rh *RequestHandler) GetSymbolDecl(use *SymbolLoc, res *SymbolLoc) error {
	dec := rh.db.GetSymbolDecl(use)
	if dec != nil {
		*res = *dec
		return nil
	} else {
		return fmt.Errorf("Symbol use not found")
	}
}

func (rh *RequestHandler) GetSymbolUses(use *SymbolLoc, res *[]*SymbolLoc) error {
	uses := rh.db.GetSymbolUses(use)
	if len(uses) > 0 {
		*res = uses
		return nil
	} else {
		return fmt.Errorf("Symbol use not found")
	}
}

func (rh *RequestHandler) GetSymbolDef(use *SymbolLoc, res *[]*SymbolLoc) error {
	def := rh.db.GetSymbolDef(use)
	if def == nil {
		// find all definitions with the same name
		defs := rh.db.GetAllSymbolDefs(use)
		if len(defs) > 0 {
			*res = defs
			return nil
		} else {
			return fmt.Errorf("Definition not found")
		}
	} else {
		*res = []*SymbolLoc{def}
		return nil
	}
}
