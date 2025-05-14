#!/bin/bash

set -ux

if [ "${1:-}" != 'destroy' ]; then

  aws cloudformation deploy \
    --stack-name use-nat-instance \
    --template-file use-nat-instance.yaml \
    --region us-east-1 \
    --capabilities CAPABILITY_AUTO_EXPAND CAPABILITY_NAMED_IAM
  
  rm -f lambda.zip
  zip -r lambda.zip replace_nat_with_nat_instance.py
  
  aws lambda update-function-code --function-name use-nat-instance-function --zip-file fileb://lambda.zip
  
  for region in us-east-2 us-west-1 us-west-2; do
    aws cloudformation deploy \
      --stack-name use-nat-instance-forwarder \
      --template-file use-nat-instance-forwarders.yaml \
      --capabilities CAPABILITY_NAMED_IAM \
      --region $region
  done

else

  aws cloudformation delete-stack --stack-name use-nat-instance --region us-east-1
  for region in us-east-2 us-west-1 us-west-2; do
    aws cloudformation delete-stack \
      --stack-name use-nat-instance-forwarder \
      --region $region
  done

fi
