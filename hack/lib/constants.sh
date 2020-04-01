#!/bin/bash

# This script provides constants for the Golang binary build process

readonly OS_GO_PACKAGE=github.com/openshift/ci-tools

readonly OS_OUTPUT_BASEPATH="${OS_OUTPUT_BASEPATH:-_output}"
readonly OS_BASE_OUTPUT="${OS_ROOT}/${OS_OUTPUT_BASEPATH}"
readonly OS_OUTPUT_SCRIPTPATH="${OS_OUTPUT_SCRIPTPATH:-"${OS_BASE_OUTPUT}/scripts"}"

readonly OS_OUTPUT_SUBPATH="${OS_OUTPUT_SUBPATH:-${OS_OUTPUT_BASEPATH}/local}"
readonly OS_OUTPUT="${OS_ROOT}/${OS_OUTPUT_SUBPATH}"

readonly OS_REQUIRED_GO_VERSION="1.13"

# os::util::list_go_src_files lists files we consider part of our project
# source code, useful for tools that iterate over source to provide vet-
# ting or linting, etc.
#
# Globals:
#  None
# Arguments:
#  None
# Returns:
#  None
function os::util::list_go_src_files() {
    find . -not \( \
        \( \
        -wholename './_output' \
        -o -wholename './.*' \
        -o -wholename '*/vendor/*' \
        \) -prune \
    \) -name '*.go' | sort -u
}
readonly -f os::util::list_go_src_files

# os::util::list_go_src_dirs lists dirs in origin/ and cmd/ dirs excluding
# doc.go useful for tools that iterate over source to provide vetting or 
# linting, or for godep-save etc.
#
# Globals:
#  None
# Arguments:
#  None
# Returns:
#  None
function os::util::list_go_src_dirs() {
    os::util::list_go_src_files | cut -d '/' -f 1-2 | grep -v ".go$" | grep -v "^./cmd" | LC_ALL=C sort -u
    os::util::list_go_src_files | grep "^./cmd/"| cut -d '/' -f 1-3 | grep -v ".go$" | LC_ALL=C sort -u
}
readonly -f os::util::list_go_src_dirs

# os::util::list_test_packages_under lists all packages containing Golang test files that we 
# want to run as unit tests under the given base dir in the source tree
function os::util::list_test_packages_under() {
    local basedir=$*

    # we do not quote ${basedir} to allow for multiple arguments to be passed in as well as to allow for
    # arguments that use expansion, e.g. paths containing brace expansion or wildcards
    find ${basedir} -not \(                   \
        \(                                    \
              -path 'vendor'                  \
              -o -path '*_output'             \
              -o -path '*.git'                \
              -o -path '*vendor/*'            \
              -o -path '*test/*'              \
        \) -prune                             \
    \) -name '*_test.go' | xargs -n1 dirname | sort -u | xargs -n1 printf "${OS_GO_PACKAGE}/%s\n"
}
readonly -f os::util::list_test_packages_under
