#!/bin/sh

### expirationDate is always with timezone +00:00, eg, "2019-07-23T22:35+0000"
MY_DATE=$(TZ=":Africa/Abidjan" date '+%Y-%m-%d')
declare -a regions=("us-east-1" "us-east-2" "us-west-1")

handle_cluster () {
  local cluster_name;
  cluster_name=$1
  echo "handling cluster: ${cluster_name} ..."
}

for r in "${regions[@]}"
do
  echo "doing region ${r} ..."
  aws ec2 describe-vpcs --output json --region "${r}" | jq --arg date "${MY_DATE}" -r -S '.Vpcs[] | select (.Tags[]? | (.Key == "expirationDate" and .Value < $date)) | .Tags[] | select (.Value == "owned") | .Key' | while read line; do handle_cluster ${line}; done
done
