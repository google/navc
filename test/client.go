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
)

func main() {
    conn, err := net.Dial("unix", "/tmp/navc.sock")
    if err != nil {
        log.Fatal("dial socket", err)
    }
    defer conn.Close()

    codec := jsonrpc.NewClientCodec(conn)
    defer codec.Close()

    client := rpc.NewClientWithCodec(codec)
    defer client.Close()

    // sample call
    args := Symbol{"", "", "sample/a.c", 16, 2}
    var reply Symbol
    err = client.Call("RequestHandler.GetSymbolDecl",
        &args,
        &reply)
    if err != nil {
        log.Fatal("calling ", err)
    }

    log.Println(reply)
}
