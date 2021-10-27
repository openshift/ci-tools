package main

import (
	"fmt"
	"github.com/anaskhan96/soup"
)

const html = `<h2>Changes from <a target="_blank" href="/releasetag/4.10.0-0.nightly-2021-10-25-125739">4.10.0-0.nightly-2021-10-25-125739</a></h2>

<p>Created: 2021-10-25 19:07:50 +0000 UTC</p>

<p>Image Digest: <code>sha256:948de0d8ed99cacd7224fb2d543ac7d01effb08eae1959fdd6c500fa6c2b543c</code></p>

<h3>Components</h3>

<ul>
<li>Kubernetes 1.22.1</li>
<li>Red Hat Enterprise Linux CoreOS <a target="_blank" href="https://releases-rhcos-art.cloud.privileged.psi.redhat.com/?release=410.84.202110220321-0&amp;stream=releases%2Frhcos-4.10">410.84.202110220321-0</a></li>
</ul>

<h3>New images</h3>

<ul>
<li><a target="_blank" href="https://github.com/openshift/cluster-api-provider-powervs">powervs-machine-controllers</a> git <a target="_blank" href="https://github.com/openshift/cluster-api-provider-powervs/commit/6c9314f0dcc3aa11f65fec8e041aa742cce42925">6c9314f0</a> <code>sha256:4d493f7a30d5c07f45f17da60d33754045f933478b5b024fd40a78e3a1a5b448</code></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/csi-driver-shared-resource-operator/tree/19ba6868922ccf0abf9b261e6e152d8d7a96ae21">csi-driver-shared-resource-operator</a></h3>

<ul>
<li>BUILD-365: Updating Dockerfile to copy needed CRDs to assets folder for install <a target="_blank" href="https://github.com/openshift/csi-driver-shared-resource-operator/pull/13">#13</a></li>
<li>BUILD-238: metrics for total shares of config maps and secrets <a target="_blank" href="https://github.com/openshift/csi-driver-shared-resource-operator/pull/12">#12</a></li>
<li><a target="_blank" href="https://github.com/openshift/csi-driver-shared-resource-operator/compare/9d2abeceed326cf9c25d910c55f4bee188f230fa...19ba6868922ccf0abf9b261e6e152d8d7a96ae21">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/machine-api-operator/tree/d3d0430fc9c77d3666f608352bd1b513626f39cc">machine-api-operator</a></h3>

<ul>
<li>PowerVS support <a target="_blank" href="https://github.com/openshift/machine-api-operator/pull/923">#923</a></li>
<li><a target="_blank" href="https://github.com/openshift/machine-api-operator/compare/e56ef220bef6b22b635c28d35c97e261ec13a550...d3d0430fc9c77d3666f608352bd1b513626f39cc">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/origin/tree/26abe83d7d00f3e5cef32b59a0ef1b4e76da1752">tests</a></h3>

<ul>
<li>RBAC default rules test: allow new configmap to authenticated users <a target="_blank" href="https://github.com/openshift/origin/pull/26524">#26524</a></li>
<li><a target="_blank" href="https://github.com/openshift/origin/compare/dc1ee70b3a11f6dc2e08b3bcd022a2d67ceb3d3f...26abe83d7d00f3e5cef32b59a0ef1b4e76da1752">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/vsphere-problem-detector/tree/7b9c861abbb6b27591f288c93a2021136b112754">vsphere-problem-detector</a></h3>

<ul>
<li>SPLAT-246: Specify user agent string for communication with vsphere <a target="_blank" href="https://github.com/openshift/vsphere-problem-detector/pull/58">#58</a></li>
<li><a target="_blank" href="https://github.com/openshift/vsphere-problem-detector/compare/c784fb91378cca1e94007cd4c408aa157ab0cd80...7b9c861abbb6b27591f288c93a2021136b112754">Full changelog</a></li>
</ul>`

func main() {
	fmt.Println("hello world")
}