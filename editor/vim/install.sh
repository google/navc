#/bin/bash
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

# This script install/uninstall the navc vim plugin. To install the plugin:
#    ./install.sh
# To uninstall the plugin:
#    ./install.sh -u
#
# To update the plugin, simply re-install it.

PLUGIN_DIR=~/.vim/plugin/navc/

if [ "$1" = "-u" ]; then
	rm -rf $PLUGIN_DIR
	exit 0
fi

mkdir -p $PLUGIN_DIR
cp navc.vim *.py $PLUGIN_DIR
