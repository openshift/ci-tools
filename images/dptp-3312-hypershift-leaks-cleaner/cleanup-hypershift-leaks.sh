#!/bin/bash
NOW=$(date +%s)
echo Cleanning up resources older than $(date -u -d @$NOW +%Y-%m-%dT%H:%M:%SZ)

echo 'Deleting expired OpenID Connect Providers...'
# Get all OpenID Connect Providers with ARN
arns=`aws iam list-open-id-connect-providers --query 'OpenIDConnectProviderList[*].Arn' --output json | jq -r '.[]'`
for arn in $arns; do
    expirationDate=$(aws iam get-open-id-connect-provider --open-id-connect-provider-arn $arn --query 'Tags[?Key==`expirationDate`].Value' --output text)
    # If command failed or the value of tag 'expirationDate' is less than today, delete the OpenID Connect Provider
    if [ $? -ne 0 ] || [ -z "$expirationDate" ]; then
        echo "Skipping $arn with no expirationDate..."
        continue
    fi

    expUnix=$(date -d $expirationDate +%s)
    if [ $expUnix -lt $NOW ]; then
        echo "Deleting OpenID connect provider $arn with expirationDate $expirationDate..."
        aws iam delete-open-id-connect-provider --open-id-connect-provider-arn $arn
    fi
done

echo 'Deleting expired IAM Roles...'
# Get all IAM Roles with ARN
names=`aws iam list-roles --query 'Roles[*].RoleName' --output json | jq -r '.[]'`
for name in $names; do
    expirationDate=$(aws iam get-role --role-name $name --query 'Role.Tags[?Key==`expirationDate`].Value' --output text)
    # If the value of tag 'expirationDate' is less than today, delete the IAM Role
    if [ $? -ne 0 ] || [ -z "$expirationDate" ]; then
        echo "Skipping $name with no expirationDate..."
        continue
    fi

    expUnix=$(date -d $expirationDate +%s)
    if [ $expUnix -lt $NOW ]; then
        # remove attached policies
        policies=`aws iam list-attached-role-policies --role-name $name --query 'AttachedPolicies[*].PolicyArn' --output json | jq -r '.[]'`
        for policy in $policies; do
            echo "Detaching policy $policy from $name..."
            aws iam detach-role-policy --role-name $name --policy-arn $policy
        done

        # remove inline policies
        policies=`aws iam list-role-policies --role-name $name --query 'PolicyNames[*]' --output json | jq -r '.[]'`
        for policy in $policies; do
            echo "Deleting inline policy $policy from $name..."
            aws iam delete-role-policy --role-name $name --policy-name $policy
        done

        # remove instance-profiles
        profiles=`aws iam list-instance-profiles-for-role --role-name $name --query 'InstanceProfiles[*].InstanceProfileName' --output json | jq -r '.[]'`
        for profile in $profiles; do
            echo "Removing instance profile $profile from $name..."
            aws iam remove-role-from-instance-profile --role-name $name --instance-profile-name $profile
        done

        echo "Deleting role $name with expirationDate $expirationDate..."
        aws iam delete-role --role-name $name
    fi
done
