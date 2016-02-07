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
	"log"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
)

type RequestHandler struct {
	db      *SymbolsDB
	handler *rpc.Server
}

// request methods
func (rh *RequestHandler) GetSymbolDecl(use *SymbolLocReq, res *SymbolLocReq) error {
	dec := rh.db.GetSymbolDecl(use)
	if dec != nil {
		*res = *dec
		return nil
	} else {
		return fmt.Errorf("Symbol use not found")
	}
}

func (rh *RequestHandler) GetSymbolUses(use *SymbolLocReq, res *[]*SymbolLocReq) error {
	uses := rh.db.GetSymbolUses(use)
	if len(uses) > 0 {
		*res = uses
		return nil
	} else {
		return fmt.Errorf("Symbol use not found")
	}
}

func (rh *RequestHandler) GetSymbolDef(use *SymbolLocReq, res *[]*SymbolLocReq) error {
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
		*res = []*SymbolLocReq{def}
		return nil
	}
}

// connection handling methods
func NewRequestHandler(db *SymbolsDB) *RequestHandler {
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

func ListenRequests(newConn chan<- net.Conn) {
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
