regexp='^([[:alnum:]]{8}-[[:alnum:]]{4}-[[:alnum:]]{4}-[[:alnum:]]{4}-[[:alnum:]]{12}|us\-east\-1)$'
echo LEASED_RESOURCE: "${LEASED_RESOURCE}"
[[ "${LEASED_RESOURCE}" =~ $regexp ]]
