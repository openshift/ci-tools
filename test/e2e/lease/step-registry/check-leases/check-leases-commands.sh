regexp='^([[:alnum:]]{8}-[[:alnum:]]{4}-[[:alnum:]]{4}-[[:alnum:]]{4}-[[:alnum:]]{12}|us\-east\-1)$'
echo LEASE0: "${LEASE0}"
echo LEASE1: "${LEASE1}"
[[ "${LEASE0}" =~ $regexp ]]
[[ "${LEASE1}" =~ $regexp ]]
