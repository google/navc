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
	"log"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
)

// RequestHandler is the handler of all quries coming to the daemon. It is
// exported as required by the rpc packade.
type RequestHandler struct {
	db      *symbolsDB
	handler *rpc.Server
}

// GetSymbolDecls gets a symbol use location and returns the list of
// declarations for that symbol.
func (rh *RequestHandler) GetSymbolDecls(use *SymbolLocReq, res *[]*SymbolLocReq) error {
	dec, err := rh.db.GetSymbolDecl(use)
	if err != nil {
		return err
	}
	*res = dec
	return nil
}

// GetSymbolUses gets a symbol use location and returns all the uses of that
// symbol.
func (rh *RequestHandler) GetSymbolUses(use *SymbolLocReq, res *[]*SymbolLocReq) error {
	uses, err := rh.db.GetSymbolUses(use)
	if err != nil {
		return err
	}
	*res = uses
	return nil
}

// GetSymbolDef gets a symbol use location and returns the definition location
// of the symbol. If not available, it returns an error.
func (rh *RequestHandler) GetSymbolDef(use *SymbolLocReq, res *[]*SymbolLocReq) error {
	def, err := rh.db.GetSymbolDef(use)
	if err != nil {
		return err
	}
	if def == nil {
		// find all definitions with the same name
		defs, err := rh.db.GetAllSymbolDefs(use)
		if err != nil {
			return err
		}
		*res = defs
		return nil
	}
	*res = []*SymbolLocReq{def}
	return nil
}

func newRequestHandler(db *symbolsDB) *RequestHandler {
	rh := &RequestHandler{db, rpc.NewServer()}

	rh.handler.Register(rh)

	return rh
}

func (rh *RequestHandler) handleRequest(conn net.Conn) {
	codec := jsonrpc.NewServerCodec(conn)
	defer codec.Close()

	err := rh.handler.ServeRequest(codec)
	if err != nil {
		log.Println("handling request (ignoring):", err)
	}
}

func listenRequests(newConn chan<- net.Conn) {
	// socket file for communication with daemon
	socketFile := ".navc.sock"

	// start serving requests
	os.Remove(socketFile)
	lis, err := net.Listen("unix", socketFile)
	if err != nil {
		log.Panic("error opening socket", err)
	}
	defer os.Remove(socketFile)
	defer lis.Close()

	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("accepting connection (breaking):", err)
			break
		}

		newConn <- conn
	}
}
