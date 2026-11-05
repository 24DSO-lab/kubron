#!/usr/bin/env python3
"""Generates golden_tokens.json: EKS tokens produced by botocore itself, with the same hooks that
awscli/customizations/eks/get_token.py registers (x-k8s-aws-id header, ExpiresIn=60, GET), at a fixed time.
The Go tests compare the provider's tokens with these byte for byte.

  pip install botocore==1.40.0
  AWS_EC2_METADATA_DISABLED=true python3 gen_golden.py > golden_tokens.json
"""
import base64, datetime, json, sys
import botocore.auth, botocore.session

FIXED = datetime.datetime.fromtimestamp(1700000000, datetime.timezone.utc).replace(tzinfo=None)


class FrozenDT(datetime.datetime):
    @classmethod
    def utcnow(cls):
        return FIXED


botocore.auth.datetime = type("m", (), {"datetime": FrozenDT, "timedelta": datetime.timedelta, "timezone": datetime.timezone})


def token(access, secret, session, region, cluster):
    s = botocore.session.get_session()
    s.set_config_variable("sts_regional_endpoints", "regional")
    c = s.create_client("sts", region_name=region, aws_access_key_id=access, aws_secret_access_key=secret, aws_session_token=session)
    c.meta.events.register("provide-client-params.sts.GetCallerIdentity",
                           lambda params, context, **kw: context.update({"x-k8s-aws-id": params.pop("x-k8s-aws-id")}) if "x-k8s-aws-id" in params else None)
    c.meta.events.register("before-sign.sts.GetCallerIdentity",
                           lambda request, **kw: request.headers.__setitem__("x-k8s-aws-id", request.context["x-k8s-aws-id"]) if "x-k8s-aws-id" in request.context else None)
    url = c.generate_presigned_url("get_caller_identity", Params={"x-k8s-aws-id": cluster}, ExpiresIn=60, HttpMethod="GET")
    return url, "k8s-aws-v1." + base64.urlsafe_b64encode(url.encode()).decode().rstrip("=")


cases = [
    ("static keys", "AKIDEXAMPLE", "secret-example-key", None, "us-east-1", "c1"),
    ("session token", "AKIDEXAMPLE", "secret-example-key", "AQoDYXdzEJr-EXAMPLE-SESSION/TOKEN+==", "eu-west-1", "c1"),
    ("china partition", "AKIDEXAMPLE", "secret-example-key", None, "cn-north-1", "c1"),
    ("cluster name with dots", "AKIDEXAMPLE", "secret-example-key", None, "us-gov-west-1", "team.prod_1-a"),
]
out = []
for name, a, sec, sess, region, cluster in cases:
    url, tok = token(a, sec, sess, region, cluster)
    out.append({"name": name, "accessKeyId": a, "secretAccessKey": sec, "sessionToken": sess or "", "region": region,
                "cluster": cluster, "unix": 1700000000, "url": url, "token": tok})
json.dump(out, sys.stdout, indent=2)
print()
