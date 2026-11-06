// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"regexp"
	"strings"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Descriptor.Params keys used by this provider (see credential.Describe). "profile" is deliberately not read:
// a named profile is a file of the machine this process runs on, and the provider never uses those.
const (
	paramAuthenticator = "authenticator"
	paramCluster       = "cluster-name"
	paramRegion        = "region"
	paramRoleARN       = "role-arn"

	authCLI = "aws-cli"
	authIAM = "aws-iam-authenticator"
)

var (
	regionRE  = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]{1,2}$`)
	clusterRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	roleRE    = regexp.MustCompile(`^arn:(aws|aws-cn|aws-us-gov):iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]{1,512}$`)
	akidRE    = regexp.MustCompile(`^[A-Z0-9]{16,128}$`)
	secretRE  = regexp.MustCompile(`^[A-Za-z0-9/+=]{16,128}$`)
	accountRE = regexp.MustCompile(`^[0-9]{12}$`)
	roleNmRE  = regexp.MustCompile(`^[A-Za-z0-9+=,.@_-]{1,64}$`)
)

const maxSessionToken = 8192

// config is what the kubeconfig says (and what the user added). Empty region or cluster means "ask the user".
type config struct {
	region  string
	cluster string
	roleARN string
}

// newConfig reads the Params. A Descriptor that is not an EKS one, or a plugin this provider does not know,
// is credential.ErrUnsupported (the Broker then uses the exec provider). A malformed value in the kubeconfig is
// unsupported too: it cannot be fixed in the dialog, and the plugin of the user may still understand it.
func newConfig(d credential.Descriptor) (config, error) {
	if d.Kind != credential.KindEKS {
		return config{}, credential.ErrUnsupported
	}
	switch d.Params[paramAuthenticator] {
	case authCLI, authIAM:
	default:
		return config{}, credential.ErrUnsupported
	}
	c := config{region: d.Params[paramRegion], cluster: d.Params[paramCluster], roleARN: d.Params[paramRoleARN]}
	if c.region != "" {
		if err := checkRegion(c.region); err != nil {
			return config{}, unsupported(err)
		}
	}
	if c.cluster != "" && !clusterRE.MatchString(c.cluster) {
		return config{}, unsupported(errBadCluster)
	}
	if c.roleARN != "" && !roleRE.MatchString(c.roleARN) {
		return config{}, unsupported(errBadRoleARN)
	}
	if err := c.check(); err != nil {
		return config{}, unsupported(err)
	}
	return c, nil
}

// check verifies that the role belongs to the partition of the region.
func (c config) check() error {
	if c.roleARN == "" || c.region == "" {
		return nil
	}
	_, part, err := partition(c.region)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(c.roleARN, "arn:"+part+":") {
		return errBadRoleARN
	}
	return nil
}

func checkRegion(region string) error {
	if !regionRE.MatchString(region) {
		return errBadRegion
	}
	_, _, err := partition(region)
	return err
}

// partition returns the DNS domain of the AWS endpoints and the ARN partition of a region. Isolated partitions
// (iso, isob, ...) are not supported: their endpoints are not reachable from a normal network anyway.
func partition(region string) (domain, arn string, err error) {
	switch {
	case strings.Contains(region, "-iso"):
		return "", "", errPartition
	case strings.HasPrefix(region, "cn-"):
		return "amazonaws.com.cn", "aws-cn", nil
	case strings.HasPrefix(region, "us-gov-"):
		return "amazonaws.com", "aws-us-gov", nil
	}
	return "amazonaws.com", "aws", nil
}

func stsHost(region string) string {
	d, _, _ := partition(region)
	return "sts." + region + "." + d
}

// fill adds the values the user typed or the saved record kept. Values of the kubeconfig win.
func (c config) fill(region, cluster string) config {
	if c.region == "" {
		c.region = region
	}
	if c.cluster == "" {
		c.cluster = cluster
	}
	return c
}

// printableToken reports whether s is non-empty, short and made of printable ASCII only (no spaces).
func printableToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

// cleanPrincipal returns an ARN that is safe to show in the audit log, or "".
func cleanPrincipal(s string) string {
	if len(s) > 256 || !printableToken(s, 256) {
		return ""
	}
	return s
}
