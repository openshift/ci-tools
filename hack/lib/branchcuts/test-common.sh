readonly src_test_files_relative_dir="data"
readonly want_test_files_relative_dir="want"
readonly test_config_relative_path="test_config"

function branchcuts::read_test_config() {
    local -n _cfg=$1
    local _test_config_path="${2}"
    for key_value in $(cat "${_test_config_path}")
    do
        if [[ "${key_value}" =~ (.+?)=(.*) ]]
        then
            _cfg["${BASH_REMATCH[1]}"]="${BASH_REMATCH[2]}"
        fi
    done
}
readonly -f branchcuts::read_test_config

function branchcuts::run_tests_template() {
    local run_cfg_manager_fn="${1}"
    local _diffs=""
    local _exit_status=0

    for test_case in $(ls "${test_cases_dir}")
    do
        os::log::info "Test case: '${test_case}' - Starts"
        local _src_test_files_dir="${test_cases_dir}/${test_case}/${src_test_files_relative_dir}"
        local _want_test_files_dir="${test_cases_dir}/${test_case}/${want_test_files_relative_dir}"

        # Read test config from file and set some vars
        local _test_config_file_path="${test_cases_dir}/${test_case}/${test_config_relative_path}"
        declare -A _test_config
        branchcuts::read_test_config _test_config "${_test_config_file_path}"

        # Function 'run_config_manager' should be defined into the caller script
        if test $(type -t "${run_cfg_manager_fn}") == "function"
        then
            os::cmd::expect_success "${run_cfg_manager_fn} \"${_src_test_files_dir}\" _test_config"
        else
            os::log::error "function '${run_cfg_manager_fn}' is not defined"
            exit 1
        fi

        _diffs=$(diff --recursive "${_want_test_files_dir}" "${_src_test_files_dir}")
        if test $? -ne 0
        then
            os::log::info "${_diffs}"
            _exit_status=1
            os::log::error "Test case: '${test_case}' - FAILURE!"
        else
            os::log::info "Test case: '${test_case}' - SUCCESS"
        fi
    done

    return $_exit_status
}
readonly -f branchcuts::run_tests_template
