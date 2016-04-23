# Copyright 2016 Google Inc. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import json
import socket
import sys


class RequestError:

    def __init__(self, error):
        self.error = error

    def __str__(self):
        return self.error


def __get_json(method, args):
    req = {
        "jsonrpc": "1.0",
        "method": method,
        "id": "navc_request",
        "params": [args]
    }

    return json.dumps(req)


def get_res(method, args):
    req = __get_json(method, args)

    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)

    try:
        sock.connect(".navc.sock")
    except socket.error, msg:
        print >>sys.stderr, msg
        sys.exit(1)

    try:
        sock.sendall(req)
        res_str = sock.recv(1024)
    finally:
        sock.close()

    res = json.loads(res_str)

    if res["error"]:
        raise RequestError(res["error"])

    return res["result"]
