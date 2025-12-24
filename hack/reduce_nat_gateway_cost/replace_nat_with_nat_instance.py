import boto3
import botocore.exceptions
import logging
import time
from typing import Dict, List, Optional, NamedTuple

# Important: Be aware of DPP/PCO resource pruning in the OCP CI ephemeral accounts. You must request
# that resources created when installing this solution (e.g. the Lambda & policies) need to be preserved.
#
# This lambda is meant to be installed into an AWS account where you want to dynamically
# replace the use of expensive NAT Gateway network traffic with inexpensive traffic
# through a "NAT instance". A NAT Gateway is a fully managed AWS service which provides NAT functionality
# for EC2 instances on a private network. A NAT instance, in contrast, is a standard EC2 instance that is
# configured to use standard Linux facilities to provide NAT functionality.
# Traffic through a NAT Gateway is charged several cents per GB. Traffic through a
# NAT instance is nearly free (we just pay for the small EC2 instance and its public IP address).
#
# The solution works by listening to events in a AWS account where we install ephemeral OCP clusters
# for testing. An AWS service called EventBridge can be used trigger this lambda function when
# certain events occur. We are interested in this lambda reacting to several events (more on why,
# later):
# 1. The creation of NAT Gateways.
# 2. New EC2 instances starting in the account.
# 3. EC2 instances being terminated in the account.
# 4. The deletion of NAT Gateways.
#
# Using these events to trigger this lambda, we can, transparent to the installer
# or other components of the cluster, create a NAT instance and interject it into
# the running cluster resources such that it provides NAT services to nodes within
# the OCP cluster.
#
# A standard OpenShift cluster uses the following major AWS networking resources:
# 1. A virtual private cloud (VPC) which is a container for most other AWS resources. A VPC
#    resides in a specific region like us-east-1. Regions are split into availability zones (AZs)
#    which are used to provide fault tolerance within a specific AWS region.
# 2. A cluster may run in a single AZ or split itself across multiple AZs for fault tolerance.
#    For example, it may put one master node in us-east-1a, another in us-east-1b, and another
#    in us-east-1c. For each AZ for which a cluster is configured to span, the installation process will
#    create:
#    a. Two subnets. One public and one private. A public subnet is defined as one that possesses
#       an Internet Gateway (IGW). An IGW allows traffic to flow between EC2 instances attached to
#       a public subnet and the Internet IFF the EC2 instance has a public IP address assigned.
#       A private subnet does not have an IGW. EC2 Instances attached to a private subnet, even
#       if they had a public IP address cannot not communicate directly to the Internet with it.
#       Instead, these instances have private IP addresses (e.g. 10.0.29.172) and use a NAT
#       gateway to perform addresses translation to communicate on the Internet. If you curl a "what is my IP"
#       service from an OpenShift node, you will see the public IP address of the NAT gateway,
#       in the AZ for the node and not the node itself (which does not have a public ipv4 in a standard installation).
#    b. A NAT gateway attached to the private subnet.
#    c. An IGW attached to the public network.
#    d. Zero or more master or worker nodes attached to the private network.
#    e. Two RouteTables. One for the public subnet and one for the private. The RouteTable
#       tells AWS how to route network traffic from an instance attached to a subnet. The
#       most important routes in this solution are those in the private subnet's RouteTable.
#       An entry that routes traffic within the private subnet: e.g. 10.0.0.0/19 => local.
#       And an entry that routes outbound traffic to the Internet: 0.0.0.0/0 => NAT Gateway instance Id.
#       This entry is used to route traffic for everything NOT in the private subnet's CIDR.
#
#  The goal of this lambda is to replace this 0.0.0.0/0 route in the private subnet with
#  0.0.0.0/0 => EC2 NAT instance network interface (ENI) where the EC2 instance has been enabled
#  to provide NAT services.
#
# With the knowledge that ephemeral IPI clusters are being created in the AWS account, the
# lambda takes action to provision a NAT instance in each public subnet being used by a cluster.
# Once the NAT instance is ready to handle traffic, it will update the RouteTable for
# the corresponding private subnet associated with the cluster & AZ to route 0.0.0.0/0 traffic
# (i.e. traffic destined for the Internet) to itself. The NAT instance resides in the
# public network and uses the IGW to communicate to the internet on behalf of other
# instances in the cluster to the internet.
# If the instance is shutdown gracefully, it will reverse this process (by way of a systemd unit
# installed by userData on startup) and restore the former route for the NAT gateway to handle
# the traffic. As a backup for a graceful shutdown, the lambda itself will also listen for
# TerminateInstances API calls and, if a master or the NAT instance is targeted, the lambda
# will restore the NAT Gateway route.
#
# The specific actions taken by the lambda are triggered by AWS API calls reported by the AWS
# EventBridge and leading to invocations of the lambda. Those events are as follows:
# 1. CreateNatGateway (the creation of NAT Gateways). We use this as a trigger to try to start the
#    NAT instance that will run in the AZ of the NatGateway.
# 2. New instances starting in the account.
# 3. Instances being terminated in the account. We use this to detect if a master node or NAT instance is being
#    shutdown -- implying the ephemeral cluster is being destroyed -- which we use to restore the NAT Gateway route
#    and the deletion of resources created by the lambda (like the NAT instance).
# 4. DeleteNatGateway - A final attempt is made to clean up in case masters aren't terminated.
#
#  Q: Why don't all installations use NAT instances instead of NAT Gateways? The AWS Gateway
#     resource provides unique HA features that are complex to replicate. It does not require
#     any operational oversight. For example, keeping a NAT instance patched & secure requires
#     it to be updated and rebooted, but during the reboot, traffic must be routed around it to prevent a
#     network disruption. Similarly, if a NAT instance malfunctions after running for a
#     long period of time, as software tends to do, that must be detected and a procedure
#     must be in place to detect and recover from the problem.
#     Our ephemeral clusters, however, are short-lived. In the extremely unlikely situation
#     where a NAT instance fails over that short lifespan, we can tolerate a test failure .
#
#  Q: What if the NAT instance does not come up?
#  A: The change to the RouteTable is performed by the startup script run by the instance
#     (i.e. the AWS userData). If the NAT instance does not come up successfully, it will
#     not change the route and the cluster will use the NAT Gateway.
#
#  Q: What if that NAT instance / other resources crated by the lambda fail to be cleaned up by
#     the lambda?
#  A: We should monitor this, but ultimately the DPP pruner should delete
#     any stragglers.
#
# EventBridge trigger on:
#   CloudTrail APIs:
#   - CreateNatGateway
#   - DeleteNatGateway
#   - RunInstances
#   - TerminateInstances
#
# Lambda
# Timeout: 10minutes  (this is primarily to allow graceful cleanup of resources)
# Lambda Role policies required:
# - AmazonEC2FullAccess
# - AWSLambdaBasicExecutionRole
# - ssm:GetParameter (to get the latest AMI)
# - iam:PassRole for the NAT instance role (created by CloudFormation)


# Used on different resources to indicate the NAT instance
# associated with the VPC.
TAG_KEY_NAT_INSTANCE_ID = 'ci-nat-instance'
# Used to indicate the NAT gateway ID that was removed from the route table
TAG_KEY_NAT_GATEWAY_ID = 'ci-nat-gateway'
TAG_KEY_VPC_ID = 'ci-nat-vpc'
TAG_KEY_PUBLIC_SUBNET_ID = 'ci-nat-public-subnet'
TAG_KEY_PRIVATE_ROUTE_TABLE_ID = 'ci-nat-private-route-table'

# The IPI userTag which should be used to trigger the NAT instance
# replacement logic. Clusters without this tag equal to "true" will not be acted
# upon.
TAG_KEY_CI_NAT_REPLACE = 'ci-nat-replace'


class NatInstanceInfo(NamedTuple):
    instance_type: str
    ami_parameter: str


# Information about the NAT instance type to use and how to find the AMI.
NAT_INSTANCES_INFO = [
    NatInstanceInfo(instance_type='t4g.micro', ami_parameter='/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-arm64-gp2'),
    NatInstanceInfo(instance_type='t3a.micro', ami_parameter='/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2'),
    NatInstanceInfo(instance_type='t3.micro', ami_parameter='/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2'),
    NatInstanceInfo(instance_type='t2.micro', ami_parameter='/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2')
]


# The instance profile created by CloudFormation for NAT instances to use
NAT_INSTANCE_PROFILE_NAME = 'use-nat-instance-profile'

VERSION = 'v1.2'


class RequestInfoFilter(logging.Filter):
    """Custom filter to inject request_id into logs."""
    def __init__(self):
        super().__init__()
        self.request_id = 'unknown'
        self.req_context = ''

    def set_info(self, request_id: str, req_context: str = ''):
        self.request_id = request_id
        self.req_context = req_context

    def filter(self, record):
        record.request_id = self.request_id
        record.req_context = self.req_context
        return True


request_info_filter = RequestInfoFilter()


formatter = logging.Formatter(f'%(levelname)s [{VERSION}] [%(request_id)s][%(req_context)s][T%(thread)d] %(message)s')
logger = logging.getLogger()
logger.setLevel(logging.INFO)

if len(logger.handlers) > 0:
    # Lambda sets up a default handler, so just update it
    for handler in logger.handlers:
        handler.setFormatter(formatter)
        handler.addFilter(request_info_filter)
else:
    # If being run outside a lambda, setup everything
    stream_handler = logging.StreamHandler()
    stream_handler.setFormatter(formatter)
    stream_handler.addFilter(request_info_filter)
    logger.addHandler(stream_handler)


# Set to False if you want the NAT to use an
# EIP that won't change on reboot.
PERMIT_IPv4_ADDRESS_POOL_USE = True

client_cache = dict()


def get_ec2_client(region):
    """
    cache clients across lambda invocations
    """
    global client_cache
    key = f'{region}-ec2'
    if key not in client_cache:
        client_cache[key] = boto3.client("ec2", region_name=region)
    return client_cache[key]


def get_latest_amazon_linux2_ami(region, nat_instance_idx):
    global client_cache
    key = f'{region}-ami'
    if key not in client_cache:
        ssm = boto3.client('ssm', region_name=region)
        response = ssm.get_parameter(Name=NAT_INSTANCES_INFO[nat_instance_idx].ami_parameter)
        ami = response['Parameter']['Value']
        client_cache[key] = ami
    return client_cache[key]


def lambda_handler(event, context):
    request_id = context.aws_request_id
    request_info_filter.set_info(request_id)

    detail = event.get("detail", {})
    event_name = detail.get("eventName", None)
    if event_name:
        request_info_filter.set_info(request_id, req_context=event_name)

    ec2_state = detail.get("state", None)
    if ec2_state:
        request_info_filter.set_info(request_id, req_context=ec2_state)

    if "errorCode" in detail or "errorMessage" in detail:
        logger.warning(f"Skipping event {event_name} due to API error: {detail.get('errorMessage', 'Unknown error')}")
        return

    if event_name == "CreateNatGateway":
        detail = event.get("detail", {})
        nat_gateway = detail["responseElements"]["CreateNatGatewayResponse"]["natGateway"]
        nat_gateway_id = nat_gateway['natGatewayId']
        public_subnet_id = nat_gateway['subnetId']
        region = detail["awsRegion"]
        nat_gateway_tags = nat_gateway.get("tagSet", {}).get('item', [])

        if not tags_has_tag(nat_gateway_tags, TAG_KEY_CI_NAT_REPLACE, "true"):
            return

        if tags_has_tag(nat_gateway_tags, TAG_KEY_NAT_INSTANCE_ID):
            logger.warning(f'NAT gateway {nat_gateway_id} replacement was already attempted; skipping')
            return

        logger.info(f'NAT gateway {nat_gateway_id} has correct tags; creating NAT instance')
        handle_create_nat_gateway(region=region, nat_gateway_id=nat_gateway_id, public_subnet_id=public_subnet_id)

    elif event_name == "DeleteNatGateway":
        detail = event.get("detail", {})
        region = detail["awsRegion"]
        ec2_client = get_ec2_client(region)

        nat_gateway_id = detail["requestParameters"]['DeleteNatGatewayRequest']["NatGatewayId"]

        response = ec2_client.describe_nat_gateways(NatGatewayIds=[nat_gateway_id])
        nat_gateway = response["NatGateways"][0]
        nat_gateway_tags = nat_gateway.get('Tags')

        if not tags_has_tag(nat_gateway_tags, TAG_KEY_CI_NAT_REPLACE, "true"):
            return  # Exit early if not a candidate

        logger.info(f'NAT gateway {nat_gateway_id} has correct tags; cleaning up NAT instance')
        # When we start a NAT instance for a gateway, we tag it with the VPC
        # for this purpose. natgateway['VpcId'] may not return what we need
        # because the gateway is in the process of being deleted and may no
        # longer be associated with the VPC.
        vpc_id = get_tag(nat_gateway_tags, TAG_KEY_VPC_ID)
        if not vpc_id:
            logger.warning(f'NAT gateway {nat_gateway_id} does not have VPC tag; skipping cleanup')
            return
        cleanup(region, vpc_id)

    elif event_name == 'RunInstances':
        detail = event.get("detail", {})
        region = detail["awsRegion"]
        ec2_client = get_ec2_client(region)

        # When successful, RunInstances has all instances launched in the responseElements.
        for instance_response in detail["responseElements"].get('instancesSet', {}).get('items', []):
            instance_id = instance_response['instanceId']
            vpc_id = instance_response['vpcId']
            tag_set = instance_response.get('tagSet', {}).get('items', [])

            if not tags_has_tag(tag_set, TAG_KEY_CI_NAT_REPLACE, 'true'):
                return

            # We only enable for master nodes coming online. This should
            # be sufficient.
            instance_name = get_tag(tag_set, 'Name')
            cluster_role = get_tag(tag_set, 'sigs.k8s.io/cluster-api-provider-aws/role')
            if (cluster_role and cluster_role not in ['master', 'control-plane']) or (not cluster_role and (not instance_name or '-master' not in instance_name)):
                return

            logger.info(f'Running tagged instance {instance_id} since it is part of the control plane')

            # When instances are terminated, describe_instances doesn't always
            # include the VPC, so we tag instances we are enabling the NAT
            # instance in response to when they are created, so we can reverse
            # it when they are terminated.
            set_tag(ec2_client, resource_id=instance_id, key=TAG_KEY_VPC_ID, value=vpc_id)

            # Note that NAT instances don't have {TAG_KEY_CI_NAT_REPLACE}=true, so RunInstance on a NAT
            # instance won't trigger this code path.
            set_nat_instance_enabled(region, vpc_id, True)
            return

    elif event_name == 'TerminateInstances':
        detail = event.get("detail", {})
        region = detail["awsRegion"]
        ec2_client = get_ec2_client(region)

        for instance_response in detail["responseElements"].get('instancesSet', {}).get('items', []):
            instance_id = instance_response['instanceId']
            response = ec2_client.describe_instances(InstanceIds=[instance_id])
            for reservation in response.get("Reservations", []):
                for instance in reservation.get("Instances", []):
                    instance_tags = instance.get('Tags', [])
                    instance_name = get_tag(instance_tags, 'Name')
                    nat_instance_vpc_id = get_tag(instance_tags, TAG_KEY_VPC_ID)
                    if not nat_instance_vpc_id:
                        return

                    if instance_name and '-bootstrap' in instance_name:
                        # We should not terminate the NAT instance just because the bootstrap machine is
                        # being terminated.
                        return

                    logger.info(f'Terminating instance {instance_id} was tagged with NAT instance information')
                    # If this instance was tagged by RunInstances, then we also use it
                    # trigger the shutdown of the NAT instance.
                    set_nat_instance_enabled(region, nat_instance_vpc_id, False)
                    return


def set_nat_instance_enabled(region: str, vpc_id: str, enabled: bool):
    ec2_client = get_ec2_client(region)

    if enabled:
        # Within the lambda, we won't know exactly when the installer will add the 0.0.0.0/0 route
        # to the private routetable, nor exactly when the NAT instance is ready to serve traffic.
        # The NAT instance userData will set the route to itself on startup, but it could be undone by the installer
        # later depending on timing. Thus, we wait for RunInstances API calls
        # and update the route table again (i.e. in addition to the NAT instance userData) to
        # point to the NAT instance. That is, unless the NAT instance userData hasn't successfully set
        # the TAG_KEY_NAT_INSTANCE_ID tag on the routetable. If that tag has not been set to
        # an instanceId, we assume the NAT instance isn't ready. Note that the NAT instance
        # sets this tag to "" when it gracefully shuts down.

        # Find the route table associated with the VPC that have been updated by a NAT
        # instance's userData script on startup
        response = ec2_client.describe_route_tables(
            Filters=[
                {"Name": "vpc-id", "Values": [vpc_id]},
                {"Name": f"tag-key", "Values": [TAG_KEY_NAT_INSTANCE_ID]},
            ]
        )
        route_table_instances = response["RouteTables"]
        if not route_table_instances:
            logger.warning(f'Did not find a route table with a NAT instance tag for vpc {vpc_id}')
            return

        route_table = route_table_instances[0]
        route_table_tags = route_table.get('Tags', [])
        nat_instance_id = get_tag(route_table_tags, TAG_KEY_NAT_INSTANCE_ID)
        route_table_id = route_table['RouteTableId']

        if nat_instance_id:
            # Check if the route is already correctly set
            routes = route_table.get('Routes', [])
            needs_update = True
            
            for route in routes:
                if route.get('DestinationCidrBlock') == '0.0.0.0/0':
                    # Found the default route, check if it's already pointing to our NAT instance
                    if route.get('InstanceId') == nat_instance_id:
                        needs_update = False
                    break
            
            if needs_update:
                ec2_client.replace_route(
                    RouteTableId=route_table_id,
                    DestinationCidrBlock="0.0.0.0/0",
                    InstanceId=nat_instance_id
                )
                logger.info(f'Updated route for 0.0.0.0/0 to point to NAT instance {nat_instance_id}')

    else:  # Set to disabled

        # Find any route table in the VPC that has been tagged with information about
        # how to restore the NAT gateway (the NAT instances will set this tag
        # during userData startup). We want to try to restore the NAT gateway usage
        # in the route table before terminating any NAT instances in cleanup()
        try:
            response = ec2_client.describe_route_tables(
                Filters=[
                    {"Name": "vpc-id", "Values": [vpc_id]},
                    {"Name": f"tag-key", "Values": [TAG_KEY_NAT_GATEWAY_ID]},
                ]
            )
            route_table_instances = response["RouteTables"]

            if route_table_instances:
                route_table = route_table_instances[0]
                route_table_tags = route_table.get('Tags', [])
                nat_gateway_id = get_tag(route_table_tags, TAG_KEY_NAT_GATEWAY_ID)
                route_table_id = route_table['RouteTableId']

                # Check if the route is already correctly set
                routes = route_table.get('Routes', [])
                needs_update = True
                
                for route in routes:
                    if route.get('DestinationCidrBlock') == '0.0.0.0/0':
                        # Found the default route, check if it's already pointing to our NAT gateway
                        if route.get('NatGatewayId') == nat_gateway_id:
                            needs_update = False
                        break
                
                if needs_update:
                    # Point the route back to the NAT gateway.
                    ec2_client.replace_route(
                        RouteTableId=route_table_id,
                        DestinationCidrBlock="0.0.0.0/0",
                        NatGatewayId=nat_gateway_id
                    )
                    logger.info(f'Updated route for 0.0.0.0/0 to point to NAT gateway {nat_gateway_id}')

        except Exception as e:
            # We might want to understand why this failed, but it shouldn't stop us
            # from tearing down resources we've allocated.
            logger.warning(f'Error trying to restore NAT gateway: {e}')

        cleanup(region=region, vpc_id=vpc_id)


def handle_create_nat_gateway(region: str, nat_gateway_id: str, public_subnet_id: str, key_name: Optional[str] = None) -> Optional[Dict]:
    """
    Creates the NAT instance in the region and public_subnet identified. If a key_name is specified, it will
    be the configured SSH key for the instance.
    Returns the NAT description object or None
    """
    ec2_client = get_ec2_client(region)

    # In order to create the NAT instance, we need to know the public subnet (which we know
    # because the NAT Gateway calls it out), and the private subnet that will eventually
    # route to that NAT Gateway. It may take time for a RouteTable to be updated in the
    # private subnet mentioning the NAT, so instead of waiting for that, we use a heuristic
    # where OpenShift include -private in the name of the subnet. We also know it will be
    # in the same AZ as the public.
    # Get Public Subnet Details (to determine VPC & AZ)
    subnet_response = ec2_client.describe_subnets(
        Filters=[{"Name": "subnet-id", "Values": [public_subnet_id]}]
    )

    if not subnet_response["Subnets"]:
        logger.warning(f"Could not find subnet details for {public_subnet_id}")
        return None

    public_subnet = subnet_response["Subnets"][0]
    vpc_id = public_subnet["VpcId"]
    availability_zone = public_subnet["AvailabilityZone"]

    # Get all subnets in the same VPC & AZ
    all_subnets_response = ec2_client.describe_subnets(
        Filters=[
            {"Name": "vpc-id", "Values": [vpc_id]},
            {"Name": "availability-zone", "Values": [availability_zone]}
        ]
    )

    private_subnet = None
    for subnet in all_subnets_response["Subnets"]:
        subnet_name = get_tag(subnet.get('Tags', []), 'Name')
        if subnet_name and "-private" in subnet_name.lower():
            private_subnet = subnet
            break

    if not private_subnet:
        logger.warning(f'Unable to find private subnet associated with NAT: {nat_gateway_id} and public subnet: {public_subnet_id}')
        return None

    # Instantiate the VM that we will be using as a NAT
    new_nat_instance = create_nat_instance(ec2_client,
                                           vpc_id=vpc_id,
                                           nat_gateway_id=nat_gateway_id,
                                           public_subnet=public_subnet,
                                           private_subnet=private_subnet,
                                           key_name=key_name)

    new_nat_instance_id = new_nat_instance["InstanceId"]
    set_tag(ec2_client, nat_gateway_id, TAG_KEY_NAT_INSTANCE_ID, new_nat_instance_id)
    return new_nat_instance


def tags_has_tag(tags: List[Dict], key: str, value: Optional[str] = None) -> bool:
    for tag in tags:
        # Depending on whether tags are from describe or a tagSet included
        # in an eventbridge API, key and value may have different casing.
        # Check for either.
        if tag.get("Key") == key or tag.get("key") == key:
            if value is None:
                # If no value was specified, match any value
                return True
            if tag.get("Value") == value or tag.get("value") == value:
                return True
    return False


def get_tag(tags: List[Dict], key: str) -> Optional[str]:
    for tag in tags:
        # Depending on whether tags are from describe or a tagSet included
        # in an eventbridge API, key and value may have different casing.
        # Check for either.
        if tag.get("Key") == key or tag.get("key") == key:
            return tag.get("Value", tag.get('value', None))
    return None


def set_tag(ec2_client, resource_id, key, value):
    ec2_client.create_tags(Resources=[resource_id], Tags=[{"Key": key, "Value": value}])


def create_nat_security_group(ec2_client, nat_gateway_id: str, public_subnet: Dict, private_subnet: Dict) -> Optional[str]:
    """
    Attempts to create a security group for the forthcoming NAT VM instance. Returns
    the security group ID.
    """
    vpc_id = public_subnet["VpcId"]  # Both subnets belong to the same VPC
    private_cidr = private_subnet["CidrBlock"]  # Extract private subnet CIDR
    public_subnet_name = get_tag(public_subnet.get('Tags', []), 'Name')

    # Create Security Group for the instance
    sg_response = ec2_client.create_security_group(
        GroupName=f"{public_subnet_name}-ci-nat-sg",
        Description="Security group for NAT instance",
        VpcId=vpc_id,
        TagSpecifications=[{
            "ResourceType": "security-group",
            "Tags": [
                {"Key": TAG_KEY_NAT_GATEWAY_ID, "Value": nat_gateway_id},
                {'Key': TAG_KEY_VPC_ID, "Value": vpc_id},
            ]
        }]
    )
    nat_sg_id = sg_response["GroupId"]
    logger.info(f"Created NAT Security Group: {nat_sg_id}")

    # Allow inbound traffic from the private subnet
    ec2_client.authorize_security_group_ingress(
        GroupId=nat_sg_id,
        IpPermissions=[
            {
                "IpProtocol": "-1",  # Allow all traffic
                "IpRanges": [{"CidrIp": private_cidr}]
            }
        ]
    )

    # Allow outbound traffic to the internet.
    # This is the default for an SG, so we don't add it
    # ec2_client.authorize_security_group_egress(
    #     GroupId=nat_sg_id,
    #     IpPermissions=[
    #         {
    #             "IpProtocol": "-1",
    #             "IpRanges": [{"CidrIp": "0.0.0.0/0"}]
    #         }
    #     ]
    # )

    # Allow inbound SSH traffic (port 22) from any IP range
    ec2_client.authorize_security_group_ingress(
        GroupId=nat_sg_id,
        IpPermissions=[
            {
                "IpProtocol": "tcp",
                "FromPort": 22,
                "ToPort": 22,
                "IpRanges": [{"CidrIp": "0.0.0.0/0"}]
            }
        ]
    )

    return nat_sg_id


def create_nat_instance(ec2_client, vpc_id, nat_gateway_id, public_subnet: Dict, private_subnet: Dict, key_name: Optional[str] = None) -> Optional[Dict]:
    """
    Creates the desired NAT instance and returns the description
    """
    private_subnet_id = private_subnet["SubnetId"]
    public_subnet_id = public_subnet["SubnetId"]
    public_subnet_name = get_tag(public_subnet.get('Tags', []), 'Name')
    availability_zone = private_subnet['AvailabilityZone']
    # Derive the region by removing the last character from the availability zone
    region = availability_zone[:-1]

    # Tag the NAT gateway with the VPC so that we can
    # retrieve it from the NAT gateway's tags on
    # DeleteNatGateway. At least for instances, once
    # they are deleted, instance['VpcId'] is a Key
    # error because it is no longer associated with
    # the instance.
    ec2_client.create_tags(
        Resources=[nat_gateway_id],
        Tags=[
            {
                "Key": TAG_KEY_VPC_ID,
                "Value": vpc_id,
            },
        ]
    )

    # The NAT instance will operate on the route table in the private subnet
    # of this AZ. There may be no RouteTable setup yet,
    # so wait for it to appear.
    private_route_table_id = None
    for attempt in range(24):
        private_route_tables = ec2_client.describe_route_tables(Filters=[{'Name': 'association.subnet-id', 'Values': [private_subnet_id]}])
        route_tables = private_route_tables['RouteTables']
        if route_tables:
            private_route_table = route_tables[0]
            private_route_table_id = private_route_table['RouteTableId']
            break

        print(f'Waiting for private route table for private subnet: {private_subnet_id}')
        time.sleep(10)

    if not private_route_table_id:
        print(f'Timeout waiting for route table in private subnet: {private_subnet_id}')

    nat_sg_id = create_nat_security_group(ec2_client,
                                          nat_gateway_id=nat_gateway_id,
                                          public_subnet=public_subnet,
                                          private_subnet=private_subnet)

    key_pair_info = {}
    if key_name:
        key_pair_info['KeyName'] = key_name

    nat_instance = None
    nat_instance_idx = 0

    # Try different instance types if one is not supported in the region
    while nat_instance_idx < len(NAT_INSTANCES_INFO):
        try:
            instance = ec2_client.run_instances(
                ImageId=get_latest_amazon_linux2_ami(region, nat_instance_idx),
                InstanceType=NAT_INSTANCES_INFO[nat_instance_idx].instance_type,
                NetworkInterfaces=[
                    {
                        'AssociatePublicIpAddress': PERMIT_IPv4_ADDRESS_POOL_USE,
                        'SubnetId': public_subnet_id,
                        'DeviceIndex': 0,
                        'Groups': [nat_sg_id],
                    },
                ],
                IamInstanceProfile={
                    # As part of its userData, the EC2 instance will update the route table
                    # for the private network to point to itself. It needs a role to have permission
                    # to do this. The instance profile is created by CloudFormation.
                    'Name': NAT_INSTANCE_PROFILE_NAME
                },
                UserData=get_nat_instance_user_data(
                    region=region,
                    nat_gateway_id=nat_gateway_id,
                    route_table_id=private_route_table_id,
                ),
                MinCount=1,
                MaxCount=1,
                TagSpecifications=[{
                    "ResourceType": "instance",
                    "Tags": [
                        {
                            "Key": TAG_KEY_NAT_GATEWAY_ID,
                            "Value": nat_gateway_id
                        },
                        {
                            "Key": TAG_KEY_PUBLIC_SUBNET_ID,
                            "Value": public_subnet_id
                        },
                        {
                            "Key": TAG_KEY_PRIVATE_ROUTE_TABLE_ID,
                            "Value": private_route_table_id
                        },
                        {
                            "Key": TAG_KEY_VPC_ID,
                            "Value": vpc_id,
                        },
                        {
                            "Key": "Name",
                            "Value": f"{public_subnet_name}-ci-nat",
                        }
                    ]
                }],
                **key_pair_info
            )
            nat_instance = instance["Instances"][0]
            break
        except botocore.exceptions.ClientError as e:
            if e.response["Error"]["Code"] == "Unsupported":
                logger.info(f'{NAT_INSTANCES_INFO[nat_instance_idx].instance_type} is not supported in this region.')
                nat_instance_idx += 1
            else:
                raise

    # Tag the route table with the nat gateway that we are
    # going to replace with the NAT instance once it starts.
    # This simplifies restoring the value later when
    # stopping the NAT instance.
    ec2_client.create_tags(
        Resources=[private_route_table_id],
        Tags=[
            {
                "Key": TAG_KEY_NAT_GATEWAY_ID,
                "Value": nat_gateway_id
            },
            {
                "Key": TAG_KEY_VPC_ID,
                "Value": vpc_id,
            },
        ]
    )

    return nat_instance


# def restore_route_by_nat_gateway(nat_gateway_id):
#     nat_gateway = get_nat_gateway(nat_gateway_id)
#     subnet_id = nat_gateway["SubnetId"]
#     vpc_id = nat_gateway["VpcId"]
#
#     route_tables = route_client.describe_route_tables(Filters=[
#         {"Name": "vpc-id", "Values": [vpc_id]},
#         {"Name": "association.subnet-id", "Values": [subnet_id]}
#     ])
#
#     for route_table in route_tables["RouteTables"]:
#         for route in route_table["Routes"]:
#             if route["DestinationCidrBlock"] == "0.0.0.0/0":
#                 route_client.replace_route(
#                     RouteTableId=route_table["RouteTableId"],
#                     DestinationCidrBlock="0.0.0.0/0",
#                     NatGatewayId=nat_gateway_id
#                 )
#                 return


def get_nat_instance_user_data(region: str, nat_gateway_id: str, route_table_id: str):
    return f"""#!/bin/bash
export AWS_MAX_ATTEMPTS=30
export AWS_RETRY_MODE=adaptive

USER_DATA_URL="http://169.254.169.254/latest/user-data/"
    
# Polling loop to check for user data availability. We 
# need this for instance id and the permissions of the
# instance profile.
while true; do
  # Attempt to fetch user data, fail on error (non-2xx status code)
  curl --fail --silent "$USER_DATA_URL" > /dev/null

  # If curl succeeds (user data available), exit the loop
  if [ $? -eq 0 ]; then
    echo "User data is available. Proceeding with the script."
    break
  else
    # If user data is not available, retry in 5 seconds
    echo "User data not yet available. Retrying in 5 seconds..."
    sleep 5
  fi
done

INSTANCE_ID=$(curl -s http://169.254.169.254/latest/meta-data/instance-id)  # The NAT instance id
echo "Found instance id: $INSTANCE_ID"

sysctl -w net.ipv4.ip_forward=1
yum install -y iptables-services
iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
service iptables save    

# Install AWS CLI if it's not already installed
if ! command -v aws &> /dev/null; then
  echo "AWS CLI not found, installing..."
  sudo yum install -y aws-cli
fi

# Create a shutdown script to remove the tag
SHUTDOWN_SCRIPT="/usr/local/bin/clear_route_table_tag.sh"

cat <<EOF > $SHUTDOWN_SCRIPT
#!/bin/bash
# Tell subsequent invocations of the lambda that the NAT instance is no longer ready to serve NAT traffic
aws ec2 create-tags --region {region} --resources {route_table_id} --tags Key={TAG_KEY_NAT_INSTANCE_ID},Value=""
# Restore use of the NAT gateway
aws ec2 replace-route --region {region} --route-table-id {route_table_id} --destination-cidr-block 0.0.0.0/0 --nat-gateway-id {nat_gateway_id}
echo "Routing table for private subnet updated to route 0.0.0.0/0 to NAT gateway {nat_gateway_id}."
EOF
chmod +x $SHUTDOWN_SCRIPT

# Create a systemd service to execute the shutdown script
cat <<EOF > /etc/systemd/system/clear-route-tag.service
[Unit]
Description=Clear route table tag on shutdown
Before=shutdown.target reboot.target halt.target
DefaultDependencies=no

[Service]
Type=oneshot
ExecStart=$SHUTDOWN_SCRIPT
RemainAfterExit=true

[Install]
WantedBy=halt.target reboot.target shutdown.target
EOF

# Reload systemd and enable the shutdown service
systemctl daemon-reload
systemctl enable clear-route-tag.service

# Per https://medium.com/nerd-for-tech/how-to-turn-an-amazon-linux-2023-ec2-into-a-nat-instance-4568dad1778f
# source/dest must be disabled for a NAT instance. This permission must be provided in the IAM instance profile.
if aws ec2 modify-instance-attribute --region {region} --instance-id $INSTANCE_ID --no-source-dest-check; then

    # Record the NAT gateway id to swich back to if the NAT instance needs to be shutdown 
    # ungracefully. If shutdown gracefully, the NAT instance will restore the NAT automatically.
    aws ec2 create-tags --region {region} --resources {route_table_id} --tags Key={TAG_KEY_NAT_INSTANCE_ID},Value="{nat_gateway_id}"

    # Set the routing table to begin routing traffic to this instance ID.
    if aws ec2 replace-route --region {region} --route-table-id {route_table_id} --destination-cidr-block 0.0.0.0/0 --instance-id $INSTANCE_ID; then
        # Indicate to subsequent lambda invocations that the NAT instance thinks it is ready to serve traffic
        aws ec2 create-tags --region {region} --resources {route_table_id} --tags Key={TAG_KEY_NAT_INSTANCE_ID},Value=$INSTANCE_ID
        echo "Routing table for private subnet updated to route 0.0.0.0/0 to NAT instance."
    else
        echo "ERROR: Unable to set route table entry! NAT instance will not be used."
    fi
else
    echo "ERROR: Unable to disable source/dest check! NAT instance will not be used."
fi
"""


def get_eips_by_tag(ec2_client, tag_key, tag_value):
    """Fetch EIPs based on the provided tag key and value."""
    response = ec2_client.describe_addresses(
        Filters=[{
            'Name': 'tag:' + tag_key,
            'Values': [tag_value]
        }]
    )
    return response['Addresses']


def cleanup(region: str, vpc_id: str):
    """
    Cleanup must be runnable in parallel from multiple lambda invocations. For
    example, if multiple master nodes are being deleted, the lamdba will be called
    for each.
    """
    ec2_client = get_ec2_client(region)

    while True:

        # Find instances that appear to have been set up as NAT instances.
        response = ec2_client.describe_instances(
            Filters=[
                {"Name": "vpc-id", "Values": [vpc_id]},
                {"Name": f"tag-key", "Values": [TAG_KEY_NAT_GATEWAY_ID]},
            ]
        )

        non_terminated_instances = set()
        instances_to_terminate = []
        for reservation in response['Reservations']:
            all_instances = reservation['Instances']
            if all_instances:
                logger.info(f"Found {len(all_instances)} NAT instances to clean up in region {region} vpc {vpc_id}.")
                instances_to_terminate = []
                for instance in all_instances:
                    instance_id = instance['InstanceId']
                    instance_state = instance['State']['Name']
                    logger.info(f'Found instance {instance_id} in state {instance_state}')
                    if instance_state == 'terminated':
                        continue

                    # Track all non-terminated instances so we wait for shutdown to complete
                    non_terminated_instances.add(instance_id)

                    # Only terminate instances that aren't already shutting down
                    if instance_state != 'shutting-down':
                        instances_to_terminate.append(instance_id)

                # Batch terminate all instances in a single API call to avoid rate limiting
                if instances_to_terminate:
                    logger.info(f"Terminating {len(instances_to_terminate)} instances: {instances_to_terminate}")
                    ec2_client.terminate_instances(InstanceIds=instances_to_terminate)

        if instances_to_terminate:
            # If there were instances to terminate, then when those instances terminate, they will
            # trigger clean up as well. Allow them to perform the remainder of the clean up
            # to prevent too many redundant threads performing the cleanup.
            return

        eips = get_eips_by_tag(ec2_client, TAG_KEY_VPC_ID, vpc_id)
        if eips:
            logger.info(f"Found {len(eips)} EIPs to release.")
            for eip in eips:
                allocation_id = eip['AllocationId']
                logger.info(f"Releasing EIP with AllocationId {allocation_id}...")
                ec2_client.release_address(AllocationId=allocation_id)

        # Look for security groups that were created for this VPC that
        # were created for the NAT instances.
        response = ec2_client.describe_security_groups(
            Filters=[
                {"Name": "vpc-id", "Values": [vpc_id]},
                {"Name": "tag-key", "Values": [TAG_KEY_NAT_GATEWAY_ID]}
            ]
        )
        security_groups = response.get("SecurityGroups", [])
        if security_groups:
            logger.info(f"Found {len(security_groups)} security groups to delete.")
            for sg in security_groups:
                sg_id = sg['GroupId']
                try:
                    logger.info(f"Deleting security group {sg_id}...")
                    ec2_client.delete_security_group(GroupId=sg_id)
                except ec2_client.exceptions.ClientError as e:
                    error_code = e.response.get('Error', {}).get('Code', '')
                    # Some security groups cannot be deleted if they're in use
                    if error_code == 'DependencyViolation':
                        logger.info(f"Security group {sg_id} cannot be deleted due to dependencies.")
                    elif error_code == 'InvalidGroup.NotFound':
                        logger.info(f"Security group {sg_id} no longer exists.")
                    else:
                        raise

        # Check if there are still instances, EIPs, or security groups to clean up
        if not non_terminated_instances and not eips and not security_groups:
            logger.info("All resources cleaned up.")
            break

        # Wait for a short time before retrying
        logger.info("Waiting for resources to terminate or release...")
        time.sleep(10)


def configure_nat_instance_network(ec2_client, nat_gateway_id: str, nat_instance_id: str) -> Dict:
    response = ec2_client.describe_instances(InstanceIds=[nat_instance_id])
    nat_instance = response['Reservations'][0]['Instances'][0]
    vpc_id = nat_instance['VpcId']

    # Check if instance has a public IP
    network_interfaces = nat_instance.get("NetworkInterfaces", [])
    has_public_ip = any(ni.get("Association", {}).get("PublicIp") for ni in network_interfaces)
    network_interface_id = nat_instance['NetworkInterfaces'][0]['NetworkInterfaceId']

    # Disable Source/Destination Check
    # Following: https://medium.com/nerd-for-tech/how-to-turn-an-amazon-linux-2023-ec2-into-a-nat-instance-4568dad1778f
    ec2_client.modify_instance_attribute(InstanceId=nat_instance_id, SourceDestCheck={'Value': False})
    logger.info(f'Disabled source/destination check for instance {nat_instance_id} network interface {network_interface_id}')

    if has_public_ip:
        # If the instance already has a public IP address (e.g. assigned by the public subnet),
        # then we don't need an EIP.
        return nat_instance

    # Otherwise, allocate and associate an EIP with the NAT instance.
    eip_response = ec2_client.allocate_address(Domain='vpc')
    eip_allocation_id = eip_response['AllocationId']
    eip_public_ip = eip_response['PublicIp']
    logger.info(f"Elastic IP created: {eip_public_ip}")

    ec2_client.associate_address(
        NetworkInterfaceId=network_interface_id,
        AllocationId=eip_allocation_id
    )

    # Tag the EIP with instance information to help clean up when
    # the cluster is being terminated.
    ec2_client.create_tags(
        Resources=[eip_allocation_id],
        Tags=[
            {
                "Key": TAG_KEY_NAT_GATEWAY_ID,
                "Value": nat_gateway_id
            },
            {
                "Key": TAG_KEY_VPC_ID,
                "Value": vpc_id
            },
        ]
    )

    logger.info(f'Assigned EIP allocation {eip_allocation_id} to NAT instance {nat_instance_id}')
    return nat_instance


def wait_for_instance_running(ec2_client, instance_id: str, max_wait_time=600, check_interval=10):
    """
    Poll (in seconds) the EC2 instance until it is in the 'running' state.
    """
    elapsed_time = 0

    while elapsed_time < max_wait_time:
        # Describe the instance
        response = ec2_client.describe_instances(InstanceIds=[instance_id])
        instance_state = response['Reservations'][0]['Instances'][0]['State']['Name']

        logger.info(f"Instance {instance_id} is in {instance_state} state.")

        # Check if the instance is running
        if instance_state == 'running':
            logger.info(f"Instance {instance_id} is now running.")
            return True

        # Wait for the specified interval before checking again
        time.sleep(check_interval)
        elapsed_time += check_interval

    # If we reached here, it means the instance didn't reach 'running' state within the timeout
    logger.info(f"Instance {instance_id} did not reach 'running' state within {max_wait_time} seconds.")
    return False


def main():
    nat_gateway_id = 'nat-0d60ac741534e7745'
    public_subnet_id = 'subnet-0d616f2b58d035274'
    vpc_id = 'vpc-047eed9958242a576'
    region = 'us-east-1'
    example_1c_instance_id = 'i-04630fccbc17cf1a8'

    ec2_client = get_ec2_client(region)

    cleanup(region, vpc_id=vpc_id)

    nat_instance = handle_create_nat_gateway(region, nat_gateway_id=nat_gateway_id, public_subnet_id=public_subnet_id, key_name='jupierce')
    nat_instance_id = nat_instance["InstanceId"]
    is_running = wait_for_instance_running(ec2_client=ec2_client, instance_id=nat_instance_id)
    if not is_running:
        logger.info('Timeout waiting for running!')
        exit(1)
    configure_nat_instance_network(ec2_client=ec2_client, nat_gateway_id=nat_gateway_id, nat_instance_id=nat_instance_id)
    # Wait while NAT instance starts up
    logger.info('Giving the NAT instance time to run userData')
    time.sleep(120)


if __name__ == '__main__':
    main()
