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

import vim
import re
import os
import navc_client as client

fname_char = re.compile('[a-zA-Z_]')

prev_locs = []


def __find_start_cur_symbol():
    row, col = vim.current.window.cursor
    while col > 0 and fname_char.match(vim.current.buffer[row - 1][col - 1]):
        col -= 1
    return (row, col + 1)


def __get_choice():
    vim.command("call inputsave()")
    vim.command("let choice = input('Input Choice<empty to cancel>: ')")
    vim.command("call inputrestore()")
    return vim.eval("choice")


def __get_choice_int():
    # TODO: we need to make sure that a number was given and that it is
    # within boundaries.
    ch = __get_choice()
    if not ch:
        raise ValueError('No choice')
    return int(ch)


def __get_cursor_input():
    line, col = __find_start_cur_symbol()
    fname = os.path.relpath(vim.current.buffer.name)

    args = {
        "File": fname,
        "Line": line,
        "Col": col,
    }

    return args


def __move_cursor(fname, line, col):
    vim.command('edit %s' % fname)
    vim.current.window.cursor = (line, col)


def __save_and_move_cursor(fname, line, col):
    prow, pcol = vim.current.window.cursor
    pfname = vim.current.buffer.name
    prev_locs.append((pfname, prow, pcol))
    __move_cursor(fname, line, col)


def __get_file_line(fname, line):
    # TODO: this is less than optimal, but it does the trick. A vim
    # implementation may be more efficient.
    with open(fname, 'r') as f:
        return f.readlines()[line - 1].strip()


def __print_error(s):
    vim.command(':echohl Error | echo "' + str(s) + '" | echohl None')


def __print_warn(s):
    vim.command(':echohl WarningMsg | echo "' + str(s) + '" | echohl None')


def __get_multi_choice(options):
    if len(options) > 1:
        num = 1
        for op in options:
            line = __get_file_line(op['File'], op['Line'])
            print "(%2d) %s %d\n     %s" % \
                (num, op['File'], op['Line'], line)
            num += 1
        ch = __get_choice_int()
        ch -= 1
    else:
        ch = 0

    return ch


def find_cursor_decl():
    try:
        ret = client.get_res(
            "RequestHandler.GetSymbolDecls", __get_cursor_input())
        ch = __get_multi_choice(ret)
        __save_and_move_cursor(ret[ch]['File'], ret[ch][
                               'Line'], ret[ch]['Col'] - 1)
    except client.RequestError as e:
        __print_error(e)


def find_cursor_uses():
    try:
        ret = client.get_res(
            "RequestHandler.GetSymbolUses", __get_cursor_input())
        ch = __get_multi_choice(ret)
        __save_and_move_cursor(ret[ch]['File'], ret[ch][
                               'Line'], ret[ch]['Col'] - 1)
    except client.RequestError as e:
        __print_error(e)
    except ValueError:
        pass


def find_symbol_def():
    try:
        ret = client.get_res("RequestHandler.GetSymbolDef",
                             __get_cursor_input())
        ch = __get_multi_choice(ret)
        __save_and_move_cursor(ret[ch]['File'], ret[ch][
                               'Line'], ret[ch]['Col'] - 1)
    except client.RequestError as e:
        __print_error(e)
    except ValueError:
        pass


def move_cursor_to_prev():
    if len(prev_locs) > 0:
        pfname, prow, pcol = prev_locs.pop()
        __move_cursor(pfname, prow, pcol)
