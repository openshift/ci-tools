import bisect

import boto3
from botocore.exceptions import ClientError
from botocore.client import Config

from ipaddress import ip_address

# See generate_range_array.py for more information


# According to https://docs.aws.amazon.com/codeguru/detector-library/python/lambda-client-reuse/
# s3 clients can and should be reused. This allows the client to be cached in an execution
# environment and reused if possible.

# If the lambda wants to sign a URL for a bucket within the account
# it is currently executing, a vanilla client will do.
local_account_s3_client = boto3.client('s3', region_name='us-east-1', config=Config(signature_version='s3v4'))

# If the lambda wants to sign a URL for a bucket in app.ci's account,
# it should use the STS s3 client constructed below.
# create an STS client object that represents a live connection to the
# STS service. We do a lazy init on this client, because it should only
# be done in the openshift-ci-infra account and not the app.ci account.
app_ci_sts_s3_client = None

# Setup
# 1. Add internal registry CloudFront distribution to DISTRIBUTION_TO_BUCKET map in this source code.
# 2. Create a new Lambda in the Account with the bucket (x86, Python 3.8)
# 3. Use this source code for the lambda
# 4. Update the lambda role policy to include the ability to read S3 bucket; just GetObject is necessary.
# {
#     "Version": "2012-10-17",
#     "Statement": [
#         {
#             "Sid": "VisualEditor0",
#             "Effect": "Allow",
#             "Action": "s3:GetObject",
#             "Resource": "arn:aws:s3:::INTERNAL_REGISTRY_BUCKET_NAME/*"
#         }
#     ]
# }
# 5. Role must include a Trust Relationship with "edgelambda.amazonaws.com"
# {
#     "Version": "2012-10-17",
#     "Statement": [
#         {
#             "Effect": "Allow",
#             "Principal": {
#                 "Service": [
#                     "edgelambda.amazonaws.com",
#                     "lambda.amazonaws.com"
#                 ]
#             },
#             "Action": "sts:AssumeRole"
#         }
#     ]
# }
# 6. Add trigger for the function - "viewer request" for the internal registry's cloudfront distribution.

# IP ranges of EC2 in each us region. Each region must be sorted by the starting IP decimal value in each range.
AWS_EC2_REGION_IP_RANGES = {"us-east-1": [[50462720, 50462975], [50463232, 50463487], [50463488, 50463743], [50529536, 50529791], [50593792, 50594047], [50594048, 50594303], [50594304, 50594559], [50595584, 50595839], [50659328, 50667519], [52503040, 52503295], [55574528, 56623103], [63963136, 65011711], [65011712, 66060287], [263274496, 263275007], [263528448, 263530495], [263530496, 263532543], [263532544, 263536639], [263540736, 263544831], [263544832, 263548927], [263548928, 263549951], [263550976, 263553023], [263557120, 263561215], [263561216, 263565311], [263565312, 263569407], [263569408, 263577599], [263577600, 263579647], [263579648, 263581695], [263581696, 263581951], [263581952, 263582207], [263582208, 263582463], [263582464, 263582719], [263582720, 263582975], [263583232, 263583487], [263583488, 263583743], [263584000, 263584255], [263585280, 263585535], [264175616, 264241151], [264308224, 264308479], [266126336, 266127359], [266132480, 266132991], [266132992, 266133503], [266135808, 266136063], [266136064, 266136575], [266136576, 266137599], [266139648, 266140159], [266140160, 266140671], [304218112, 304226303], [304277504, 304279551], [315359232, 315621375], [315621376, 316145663], [317194240, 317456383], [387186688, 387448831], [583008256, 584056831], [585105408, 586153983], [591873024, 591874047], [597229568, 597295103], [598212608, 598736895], [750780416, 752877567], [775147520, 775148543], [839909376, 840040447], [840105984, 840171519], [872415232, 872546303], [872546304, 872677375], [872677376, 872939519], [873725952, 873988095], [875298816, 875429887], [875954176, 876085247], [877002752, 877133823], [877133824, 877264895], [878051328, 878182399], [878313472, 878444543], [878627072, 878627135], [878639104, 878639119], [878703872, 878704127], [878706512, 878706527], [885522432, 886046719], [911212544, 911736831], [911736832, 911998975], [912031744, 912064511], [915406848, 915668991], [915931136, 915996671], [916193280, 916455423], [916455424, 916979711], [917241856, 917372927], [917372928, 917503999], [918814720, 918945791], [918945792, 919011327], [919339008, 919470079], [919601152, 919732223], [919732224, 919863295], [920453120, 920518655], [920649728, 920780799], [920780800, 920911871], [921305088, 921436159], [921436160, 921567231], [921829376, 921960447], [1073116928, 1073117183], [1086029824, 1086033919], [1090273280, 1090273535], [1090273792, 1090274047], [1090274048, 1090274303], [1090274304, 1090274559], [1090274560, 1090274815], [1137311744, 1137328127], [1145204736, 1145208831], [1189633024, 1189634047], [1210646528, 1210650623], [1210851328, 1210859519], [1264943104, 1264975871], [1666023424, 1666023679], [1666023680, 1666023935], [1666029312, 1666029567], [1666038528, 1666038783], [1666039552, 1666039807], [1666055680, 1666055935], [1670776832, 1670778879], [1679294464, 1679818751], [1796472832, 1796734975], [2382667776, 2382667783], [2382667784, 2382667791], [2382667800, 2382667807], [2382667816, 2382667823], [2382667824, 2382667831], [2382667848, 2382667855], [2382667856, 2382667863], [2382667864, 2382667871], [2382667888, 2382667895], [2382667896, 2382667903], [2382667904, 2382667911], [2382667912, 2382667919], [2713485312, 2713485823], [2713485824, 2713486335], [2713486336, 2713486847], [2713487360, 2713487871], [2713488384, 2713488895], [2713488896, 2713489407], [2713489408, 2713489919], [2713489920, 2713490431], [2713490944, 2713491455], [2713491968, 2713492479], [2713492992, 2713493503], [2734353408, 2734353663], [2734353664, 2734353919], [2734353920, 2734354431], [2927689728, 2927755263], [3091742720, 3091759103], [3091759104, 3091791871], [3091791872, 3091857407], [3425501184, 3425566719], [3438067712, 3438084095], [3495319552, 3495320063], [3496882176, 3496886271], [3635863552, 3635865599], [3635865600, 3635866623], [3635867136, 3635867647]], "us-gov-east-1": [[50599936, 50601983], [318504960, 318570495], [318570496, 318636031], [318636032, 318701567], [591885056, 591885311], [878639472, 878639487], [1666037504, 1666037759], [1670864896, 1670866943], [1823423488, 1823424511]], "us-gov-west-1": [[50597888, 50599935], [52297728, 52428799], [52428800, 52494335], [264765440, 264830975], [265093120, 265158655], [591885312, 591885567], [876412928, 876478463], [878639328, 878639343], [886964224, 886996991], [1618935808, 1618968575], [1666037760, 1666038015], [1823422464, 1823423487], [2684420096, 2684485631]], "us-west-2": [[50594560, 50594815], [50594816, 50595071], [50595328, 50595583], [50678784, 50679807], [50679808, 50681855], [263278592, 263278847], [263520256, 263524351], [263524352, 263528447], [263536640, 263540735], [263549952, 263550975], [263553024, 263557119], [263582976, 263583231], [263583744, 263583999], [263584256, 263584511], [263584512, 263584767], [263584768, 263585023], [263585024, 263585279], [264308480, 264308735], [266076160, 266080255], [266080256, 266084351], [266084352, 266086399], [266086400, 266087423], [266127360, 266127871], [266127872, 266128383], [266128384, 266128639], [266128640, 266128895], [266128896, 266129151], [266129152, 266129407], [266129536, 266129599], [266133504, 266134015], [266134016, 266134271], [266140672, 266141695], [268238848, 268304383], [268304384, 268369919], [304230400, 304234495], [304280576, 304281599], [317456384, 317587455], [318111744, 318177279], [584056832, 585105407], [591872000, 591873023], [592445440, 593494015], [597360640, 597426175], [597688320, 598212607], [752877568, 754974719], [846200832, 846266367], [873070592, 873201663], [873201664, 873332735], [873988096, 874250239], [874512384, 874774527], [874774528, 875036671], [875036672, 875298815], [875475968, 875476991], [877330432, 877395967], [878182400, 878313471], [878605312, 878606335], [878639200, 878639215], [878639424, 878639439], [878700032, 878700287], [878704384, 878704639], [878706544, 878706559], [910426112, 910688255], [915668992, 915800063], [918028288, 918552575], [919076864, 919207935], [919207936, 919339007], [919863296, 919994367], [919994368, 920059903], [920256512, 920322047], [921960448, 922025983], [922025984, 922091519], [1090273536, 1090273791], [1090274816, 1090275071], [1090275072, 1090275327], [1090275328, 1090275583], [1090275584, 1090275839], [1189134336, 1189150719], [1666023936, 1666024191], [1666029568, 1666029823], [1666038272, 1666038527], [1666055424, 1666055679], [1670789120, 1670791167], [1679032320, 1679294463], [2382667792, 2382667799], [2382667808, 2382667815], [2382667832, 2382667839], [2382667840, 2382667847], [2382667872, 2382667879], [2382667880, 2382667887], [2713486848, 2713487359], [2713487872, 2713488383], [2713490432, 2713490943], [2713491456, 2713491967], [2713492480, 2713492991], [2713493504, 2713494015], [2732495872, 2732496895]], "us-west-1": [[50700288, 50701311], [56950784, 57016319], [221511680, 221577215], [221773824, 221839359], [221839360, 221904895], [263278848, 263279103], [311427072, 311558143], [591885568, 591885823], [840040448, 840105983], [872939520, 873005055], [873005056, 873070591], [875823104, 875954175], [878612480, 878612991], [878639232, 878639247], [878639440, 878639455], [878704128, 878704383], [878706528, 878706543], [910360576, 910426111], [915865600, 915898367], [915996672, 916029439], [917504000, 917635071], [917962752, 918028287], [918618112, 918683647], [920059904, 920125439], [920322048, 920387583], [921763840, 921829375], [1090287104, 1090287359], [1090287360, 1090287615], [1090287616, 1090287871], [1090287872, 1090288127], [1090288128, 1090288383], [1090288384, 1090288639], [1666024448, 1666024703], [1666030080, 1666030335], [1670871040, 1670873087], [3091726336, 3091742719], [3098116096, 3098148863], [3438051328, 3438067711], [3635866624, 3635867135]], "us-east-2": [[50692096, 50693119], [50693120, 50693631], [51118080, 51183615], [51183616, 51249151], [51249152, 51380223], [51380224, 51642367], [51642368, 51904511], [58720256, 58851327], [58851328, 58916863], [58916864, 58982399], [58982400, 59244543], [59244544, 59768831], [59768832, 60293119], [221904896, 222035967], [263275008, 263275519], [304236544, 304238591], [304282624, 304283647], [309592064, 309854207], [314310656, 314376191], [314376192, 314441727], [314441728, 314507263], [314507264, 314572799], [316145664, 316407807], [316407808, 316669951], [316669952, 316932095], [591881728, 591881983], [873332736, 873398271], [873398272, 873463807], [878639264, 878639279], [878705408, 878705663], [1090275840, 1090276095], [1090276096, 1090276351], [1090276352, 1090276607], [1090276608, 1090276863], [1666024192, 1666024447], [1666029824, 1666030079], [1670774784, 1670776831], [3328377344, 3328377599]]}

APP_CI_DISTRIBUTION = 'E2KP8SMSY4XB67'

API_CI_REPLICAS = {
    'us-east-2': 'us-east-2-replica-of-ci-dv2np-image-registry-us-east-1',
    'us-west-1': 'us-west-1-replica-of-ci-dv2np-image-registry-us-east-1',
    'us-west-2': 'us-west-2-replica-of-ci-dv2np-image-registry-us-east-1',
}

# Map distribution name to regions to redirect and then to what S3 bucket to redirect.
# If distribution or region is not found, request will stay in CloudFront.
DISTRIBUTION_TO_BUCKET = {
    APP_CI_DISTRIBUTION: {  # app.ci
        'us-east-1': 'ci-dv2np-image-registry-us-east-1-aunteqmixxpqypvdqwbmjbiloeix',
        'us-east-2': 'ci-dv2np-image-registry-us-east-1-aunteqmixxpqypvdqwbmjbiloeix',
    },
    'E2PBG0JIU6CTJY': {
        'us-east-1': 'build03-vqlvf-image-registry-us-east-1-elknekacnvmxphlxdfyvhpi',
        'us-east-2': 'build03-vqlvf-image-registry-us-east-1-elknekacnvmxphlxdfyvhpi',
    },
    'E1Q1256FT1FBYD': {
        'us-east-1': 'build01-9hdwj-image-registry-us-east-1-nucqrmelsxtgndkbvchwdkw',
        'us-east-2': 'build01-9hdwj-image-registry-us-east-1-nucqrmelsxtgndkbvchwdkw',
    },
    'E1PPY7S6SRDS9W': {
        'us-east-1': 'build05-kwk66-image-registry-us-east-1-vuabtweixqvnbprjlctjacx',
        'us-east-2': 'build05-kwk66-image-registry-us-east-1-vuabtweixqvnbprjlctjacx',
    }
}

# Set to None for normal operation and everything will route through
# CloudFront or S3. Set to your IP address, and you will be sent to
# S3 while everything else goes to CloudFront.
exclusive_test_for_ip = None


# bisect does not support key= until 3.10. Eliminate this when
# Lambda supports 3.10.
class KeyifyList(object):
    def __init__(self, inner, key):
        self.inner = inner
        self.key = key

    def __len__(self):
        return len(self.inner)

    def __getitem__(self, k):
        return self.key(self.inner[k])


def lambda_handler(event, context):
    global local_account_s3_client
    global app_ci_sts_s3_client

    request = event['Records'][0]['cf']['request']
    event_config = event['Records'][0]['cf']['config']
    distribution_name = event_config['distributionId']

    request_method = request.get('method', None)
    if request_method.lower() != "get":
        # The S3 signed URL is only for GET operations.
        # The registry itself will issue HEAD when checking for images.
        # Just let CloudFront handle these.
        return request

    regions_to_redirect = DISTRIBUTION_TO_BUCKET.get(distribution_name, None)
    if not regions_to_redirect:
        # Distribution has no associated buckets - allow CloudFront to process the request
        return request

    request_ip = request['clientIp']
    ip_as_int = int(ip_address(request_ip))

    if exclusive_test_for_ip and request_ip != exclusive_test_for_ip:
        # Someone is testing with exclusive_test_for_ip, route all
        # requests straight to CloudFront.
        return request

    found_region = None

    for region, regions_ec2_ip_ranges in AWS_EC2_REGION_IP_RANGES.items():
        # Each AWS_EC2_REGION_IP_RANGES value is sorted by the first ip address in each range.
        # Use bisect to quickly identify the range in which the incoming IP would fall
        # if it were an EC2 instance.

        # Bisect does not accept a 'key' parameter until 3.10, so make due with KeyifyList until
        # AWS Lambda supports 3.10.
        position = bisect.bisect(KeyifyList(regions_ec2_ip_ranges, lambda entry: entry[0]), ip_as_int)

        if position > 0:
            for ip_range in regions_ec2_ip_ranges[position-1:]:
                if exclusive_test_for_ip and exclusive_test_for_ip == request_ip:
                    found_region = region
                    break
                if ip_as_int >= ip_range[0] and ip_as_int <= ip_range[1]:
                    found_region = region
                    break
                if ip_as_int < ip_range[1]:
                    # Not found and there is no reason to check the next range
                    break

    if not found_region:
        # Caller does not appear to be an ec2 instance within a region we care about.
        return request

    if distribution_name != APP_CI_DISTRIBUTION and app_ci_sts_s3_client is None:
        # Only initialize the cached sts client if we are not in the app.ci account.
        sts_client = boto3.client('sts')
        # Call the assume_role method of the STSConnection object and pass the role
        # ARN and a role session name.
        assumed_role_object = sts_client.assume_role(
            RoleArn="arn:aws:iam::059165973077:role/service-role/cloudfront-us-east-to-s3-direct-role-ioj0vsn0",
            RoleSessionName="build-farm-registry-cloudfront-lambda"
        )
        # From the response that contains the assumed role, get the temporary
        # credentials that can be used to make subsequent API calls
        credentials = assumed_role_object['Credentials']
        app_ci_sts_s3_client = boto3.client('s3', region_name='us-east-1', aws_access_key_id=credentials['AccessKeyId'],
                                            aws_secret_access_key=credentials['SecretAccessKey'],
                                            aws_session_token=credentials['SessionToken'],
                                            config=Config(signature_version='s3v4'))

    uri: str = request.get('uri', '')
    s3_key = uri[1:]  # Strip '/'
    redirect_to_bucket = None

    if found_region != 'us-east-1' and uri.startswith('/docker/registry/v2/blobs/') and found_region in API_CI_REPLICAS:
        # If the request is for a blob and NOT in us-east-1, let's check to see
        # if the replicas of the app.ci internal registry S3 bucket in that caller's
        # region. If the caller is in us-east-1, all of our build farms have their
        # s3 bucket in us-east-1, so let the caller go there instead of trying to find
        # the resource in app.ci's replicas.
        # Note that we never do this for /docker/repositories/* as these track tags.
        # Tags can point to different, cluster specific images (whereas a blob
        # sha uniquely and permanently defines its content), so we don't want to draw tags from replicas.
        # Replicas can take up to 15 minutes to be synchronized -- so another reason to never rely on
        # tag values.

        # find the name of app.ci's internal registry replicated bucket in us-west-1
        ideal_replica_bucket = API_CI_REPLICAS[found_region]

        if distribution_name == APP_CI_DISTRIBUTION:
            # This lambda is operating in the app.ci account and will use the role
            # associated with the lambda to read s3.
            s3 = local_account_s3_client
        else:
            # This lambda is operating in another account. We need to use STS
            # to assume a role in the app.ci account so that we can read the
            # replica buckets in that account.

            s3 = app_ci_sts_s3_client

        try:
            s3.head_object(Bucket=ideal_replica_bucket, Key=s3_key)
            # The blob was found. Use the replica bucket.
            redirect_to_bucket = ideal_replica_bucket
        except ClientError:
            pass

    if not redirect_to_bucket:
        # If we reach this point, the lambda should try to talk to the bucket associated
        # with this distribution & region.
        redirect_to_bucket = regions_to_redirect.get(found_region, None)
        if not redirect_to_bucket:
            # Distribution does not want to redirect caller's region to S3.
            return request

        # Prep the client to talk to the distribution's / cluster's us-east-1
        # internal registry bucket.
        s3 = local_account_s3_client

    # Depending on the codepath here, s3 may be an STS assumed role client
    # to app.ci or just a direct client to the distribution's underlying s3 bucket.
    # Generate a signed URL for the caller to go back to S3 and read the object
    url = s3.generate_presigned_url(
        ClientMethod='get_object',
        Params={
            'Bucket': redirect_to_bucket,
            'Key': s3_key,
        },
        ExpiresIn=20*60,  # Expire in 20 minutes
    )

    return {
        'status': '307',
        'statusDescription': 'CostManagementRedirect',
        'headers': {
            'location': [
                {
                    'value': url
                }
            ],
        },
    }
