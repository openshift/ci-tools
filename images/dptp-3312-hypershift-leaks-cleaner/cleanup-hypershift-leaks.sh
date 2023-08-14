#!/bin/bash
NOW=$(date +%s)
echo Cleanning up resources older than $(date -u -d @$NOW +%Y-%m-%dT%H:%M:%SZ)
AWS_CRED_PATH=${AWS_CRED_PATH:-/token/.awscred}
echo Using AWS_CRED_PATH=${AWS_CRED_PATH}

echo 'Deleting broken HostedClusters...'
# Get json output from oc command
oc -n clusters get hostedclusters -o json | jq -r '.items[] | select(.metadata.annotations.broken == "true" and .spec.dns.baseDomain == "hypershift.aws-2.ci.openshift.org" ) | "\(.metadata.namespace) \(.metadata.name)"' | while read cluster; do
    echo 'Deleting' $cluster
    echo $cluster | awk -v AWS_CRED_PATH="$AWS_CRED_PATH" '{ print "timeout 5m hypershift destroy cluster aws --name " $2 " --aws-creds " AWS_CRED_PATH " --destroy-cloud-resources " }' | bash
done

echo 'Deleting expired HostedClusters...'
# Get json output from oc command
clusters_json=$(oc -n clusters get hostedclusters -o json | jq '[.items[] | select( .spec.dns.baseDomain == "hypershift.aws-2.ci.openshift.org" ) | .metadata]')
# Process each cluster
echo "${clusters_json}" | jq -c '.[]' | while read cluster; do
    # Extract creationTimestamp and cluster name
    creation_time_str=$(echo "${cluster}" | jq -r ".creationTimestamp")
    cluster_name=$(echo "${cluster}" | jq -r ".name")

    # Convert creationTimestamp to seconds (Unix timestamp)
    creation_time=$(date --date="${creation_time_str}" +%s)

    # Calculate the time difference in hours
    time_diff_hr=$(( (NOW - creation_time) / 3600 ))

    # If creationTimestamp is older than 12 hours, delete the cluster
    if (( time_diff_hr > 12 )); then
        echo "Deleting cluster ${cluster_name} created at ${creation_time_str}..."
        timeout 5m hypershift destroy cluster aws --name "${cluster_name}" --aws-creds ${AWS_CRED_PATH} --destroy-cloud-resources
    fi
done

# echo 'Patching clusters stuck at deleting'
# oc get hc -A -o json --as system:admin | jq -r '.items[] | select(.metadata.finalizers == ["openshift.io/destroy-cluster"]) | "\(.metadata.namespace) \(.metadata.name)"' | awk '{ print "oc patch -n " $1 " hostedclusters/" $2 " -p '\''{\"metadata\":{\"finalizers\":[]}, \"status\":{\"version\":{\"availableUpdates\":[]}}}'\'' --type=merge --as=system:admin"  }' | bash

echo 'Deleting expired OpenID Connect Providers...'
# Get all OpenID Connect Providers with ARN
arns=`aws iam list-open-id-connect-providers --query 'OpenIDConnectProviderList[*].Arn' --output json | jq -r '.[]'`
for arn in $arns; do
    expirationDate=$(aws iam get-open-id-connect-provider --open-id-connect-provider-arn $arn --query 'Tags[?Key==`expirationDate`].Value' --output text)
    # if the value of `expirationDate` is `None` or empty or the command is failed
    # then skip
    if [ $? -ne 0 ] || [ -z "$expirationDate" ]; then
        echo "Skipping $arn with no expirationDate..."
        continue
    fi
    if [ "$expirationDate" == "None" ]; then
        echo "Skipping $arn with expirationDate None... $expirationDate"
        continue
    fi

    expUnix=$(date -d $expirationDate +%s)
    if [ $? -ne 0 ] || [ -z $expUnix ]; then
        echo "Skipping $name with invalid expirationDate..."
        continue
    fi
    if [ $expUnix -lt $NOW ]; then
        echo "Deleting OpenID connect provider $arn with expirationDate $expirationDate..."
        aws iam delete-open-id-connect-provider --open-id-connect-provider-arn $arn
    fi
done

echo 'Deleting expired IAM Roles...'
# Get all IAM Roles with ARN
names=`aws iam list-roles --query 'Roles[*].RoleName' --output json | jq -r '.[]'`
for name in $names; do
    # if name starts with 'ci-' then skip
    if [[ $name == ci-* ]]; then
        echo "Skipping $name..."
        continue
    fi
    expirationDate=$(aws iam get-role --role-name $name --query 'Role.Tags[?Key==`expirationDate`].Value' --output text)
    # If the value of tag 'expirationDate' is less than today, delete the IAM Role
    if [ $? -ne 0 ] || [ -z "$expirationDate" ]; then
        echo "Skipping $name with no expirationDate..."
        continue
    fi
    if [ "$expirationDate" == "None" ]; then
        echo "Skipping $arn with expirationDate None... $expirationDate"
        continue
    fi

    expUnix=$(date -d $expirationDate +%s)
    if [ $? -ne 0 ] || [ -z $expUnix ]; then
        echo "Skipping $name with invalid expirationDate..."
        continue
    fi
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

echo 'Deleting expired Route53 zones...'
# Get all Route53 hosted zones within ".hypershift.local" domain
zones=$(aws route53 list-hosted-zones --query 'HostedZones[?ends_with(Name, `hypershift.local.`) || ends_with(Name, `.hypershift.aws-2.ci.openshift.org.`)].{Id:Id, Name:Name}' --output json | jq -c '.[]')

for zone in $zones; do
    zone_id=$(echo $zone | jq -r '.Id' | cut -d '/' -f 3)
    zone_name=$(echo $zone | jq -r '.Name')

    # Get the creationDate value from the openshift_creationDate tag
    creationDate=$(aws route53 list-tags-for-resource --resource-type hostedzone --resource-id $zone_id --query 'ResourceTagSet.Tags[?Key==`openshift_creationDate`].Value' --output text)

    # Skip if there is no creationDate or the command failed
    if [ $? -ne 0 ] || [ -z "$creationDate" ]; then
        echo "Skipping $zone_name with no openshift_creationDate..."
        continue
    fi

    # Convert the creationDate to a Unix timestamp
    creationUnix=$(date -d $creationDate +%s)
    if [ $? -ne 0 ] || [ -z $creationUnix ]; then
        echo "Skipping $zone_name with invalid openshift_creationDate..."
        continue
    fi

    # Calculate the age of the zone in seconds
    zone_age_seconds=$((NOW - creationUnix))

    # Check if the zone is older than 12hr (43200 seconds)
    if [ $zone_age_seconds -gt 43200 ]; then
        # Get the resource record sets in the hosted zone
        record_sets=$(aws route53 list-resource-record-sets --hosted-zone-id $zone_id --query 'ResourceRecordSets[?Type != `SOA` && Type != `NS`]' --output json | jq -c '.[]')

        # Delete the resource record sets
        for record_set in $record_sets; do
            record_name=$(echo $record_set | jq -r '.Name')
            record_type=$(echo $record_set | jq -r '.Type')

            echo "Deleting resource record set $record_name ($record_type) in zone $zone_name..."
            aws route53 change-resource-record-sets --hosted-zone-id $zone_id --change-batch "{\"Changes\": [{\"Action\": \"DELETE\", \"ResourceRecordSet\": $record_set}]}"
        done

        echo "Deleting Route53 zone $zone_name with openshift_creationDate $creationDate..."
        aws route53 delete-hosted-zone --id $zone_id
    else
        echo "Skipping $zone_name with openshift_creationDate $creationDate..."
    fi
done
