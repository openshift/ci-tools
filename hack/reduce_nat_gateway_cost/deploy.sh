#!/bin/bash

set -ux

# Change to the directory where this script is located
cd "$(dirname "$0")"

usage() {
  echo "Usage: $0 <aws-profile> [destroy]"
  echo "  aws-profile: AWS CLI profile name to use"
  echo "  destroy:     Optional - if specified, deletes the stack instead of deploying"
  exit 1
}

if [ -z "${1:-}" ]; then
  usage
fi

AWS_PROFILE="$1"
ACTION="${2:-deploy}"

if [ "$ACTION" != 'destroy' ]; then

  aws --profile "$AWS_PROFILE" cloudformation deploy \
    --stack-name use-nat-instance \
    --template-file use-nat-instance.yaml \
    --region us-east-1 \
    --capabilities CAPABILITY_AUTO_EXPAND CAPABILITY_NAMED_IAM
  
  rm -f lambda.zip
  zip -r lambda.zip replace_nat_with_nat_instance.py
  
  aws --profile "$AWS_PROFILE" lambda update-function-code \
    --function-name use-nat-instance-function \
    --zip-file fileb://lambda.zip \
    --region us-east-1
  
  for region in us-east-2 us-west-1 us-west-2; do
    aws --profile "$AWS_PROFILE" cloudformation deploy \
      --stack-name use-nat-instance-forwarder \
      --template-file use-nat-instance-forwarders.yaml \
      --capabilities CAPABILITY_NAMED_IAM \
      --region $region
  done

else

  # Delete the Lambda function explicitly since it has DeletionPolicy: Retain.
  # This is the critical resource - without it, NAT instance replacement stops.
  echo "Deleting Lambda function use-nat-instance-function..."
  aws --profile "$AWS_PROFILE" lambda delete-function \
    --function-name use-nat-instance-function \
    --region us-east-1 2>/dev/null || echo "Lambda function not found or already deleted"

  # Delete CloudFormation stacks (other resources will be retained due to DeletionPolicy)
  aws --profile "$AWS_PROFILE" cloudformation delete-stack --stack-name use-nat-instance --region us-east-1
  for region in us-east-2 us-west-1 us-west-2; do
    aws --profile "$AWS_PROFILE" cloudformation delete-stack \
      --stack-name use-nat-instance-forwarder \
      --region $region
  done

  echo ""
  echo "NOTE: Other resources (IAM roles, instance profile, log group, event rule) were retained."
  echo "To fully clean up, manually delete these resources or redeploy without DeletionPolicy: Retain."

fi
