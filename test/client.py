# Copyright 2015 Google Inc. All Rights Reserved.
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

"""
Sample python file to call rpc functions to the navc daemon. It should be called
from the test directory as:
  PYTHONPATH=../third_party/jsonrpc/ python client.py
"""

import jsonrpc

server = jsonrpc.ServerProxy(jsonrpc.JsonRpc10(),
         jsonrpc.TransportUnixSocket(addr='/tmp/navc.sock', logfunc=jsonrpc.log_stdout))

args = {
    "File": "sample/a.c",
    "Line": 16,
    "Col": 2,
}
ret = server.RequestHandler.GetSymbolDecl(args)
print ret

args = {
    "File": "sample/a.c",
    "Line": 14,
    "Col": 2,
}
ret = server.RequestHandler.GetSymbolUses(args)
print ret
