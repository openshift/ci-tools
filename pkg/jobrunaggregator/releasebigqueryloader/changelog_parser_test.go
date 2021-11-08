package releasebigqueryloader

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/anaskhan96/soup"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

const simpleChangelog = `
<h2>Changes from <a target="_blank" href="/releasetag/4.10.0-0.nightly-2021-10-25-062528">4.10.0-0.nightly-2021-10-25-062528</a></h2>

<p>Created: 2021-10-25 12:59:13 +0000 UTC</p>

<p>Image Digest: <code>sha256:ce49e1580d0f7294b01211213cd8f2e96919edcbe59adf2f60c082b838bbee45</code></p>

<h3>Components</h3>

<ul>
<li>Kubernetes 1.22.1</li>
<li>Red Hat Enterprise Linux CoreOS <a target="_blank" href="https://releases-rhcos-art.cloud.privileged.psi.redhat.com/?release=410.84.202110220321-0&amp;stream=releases%2Frhcos-4.10">410.84.202110220321-0</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/kuryr-kubernetes/tree/ae2b8f2f5ae3e4cce74055b44821b502dccf4e27">kuryr-cni, kuryr-controller</a></h3>

<ul>
<li>Rebase openshift/kuryr-kubernetes from <a target="_blank" href="https://opendev.org/openstack/kuryr-kubernetes">https://opendev.org/openstack/kuryr-kubernetes</a> <a target="_blank" href="https://github.com/openshift/kuryr-kubernetes/pull/583">#583</a></li>
<li><a target="_blank" href="https://github.com/openshift/kuryr-kubernetes/compare/ce810161cdae640b7c0cfe7e0b631cf09424270e...ae2b8f2f5ae3e4cce74055b44821b502dccf4e27">Full changelog</a></li>
</ul>`

const rejectedWithCoreOSUpgrade = `
<h2>Changes from <a target="_blank" href="/releasetag/4.8.0-0.nightly-2021-10-20-155651">4.8.0-0.nightly-2021-10-20-155651</a></h2>

<p>Created: 2021-10-27 12:32:48 +0000 UTC</p>

<p>Image Digest: <code>sha256:156597f339b3f4c953e1d8ef9d0881c68aacc291731f5dbf4d9ee17c84116846</code></p>

<h3>Components</h3>

<ul>
<li>Kubernetes 1.21.4</li>
<li>Red Hat Enterprise Linux CoreOS upgraded from <a target="_blank" href="https://releases-rhcos-art.cloud.privileged.psi.redhat.com/?release=48.84.202110152059-0&amp;stream=releases%2Frhcos-4.8">48.84.202110152059-0</a> to <a target="_blank" href="https://releases-rhcos-art.cloud.privileged.psi.redhat.com/?release=48.84.202110270303-0&amp;stream=releases%2Frhcos-4.8">48.84.202110270303-0</a> (<a target="_blank" href="https://releases-rhcos-art.cloud.privileged.psi.redhat.com/diff.html?arch=x86_64&amp;first_release=48.84.202110152059-0&amp;first_stream=releases%2Frhcos-4.8&amp;second_release=48.84.202110270303-0&amp;second_stream=releases%2Frhcos-4.8">diff</a>)</li>
</ul>

<h3>Rebuilt images without code change</h3>

<ul>
<li><a target="_blank" href="https://github.com/openshift/vsphere-problem-detector">vsphere-problem-detector</a> git <a target="_blank" href="https://github.com/openshift/vsphere-problem-detector/commit/c02283d6c979f4e63b76945951272b1cfd134c32">c02283d6</a> <code>sha256:41073e65c30550072248a075952ef6383dfd42a39cc1e302c0d94c262d59dff7</code></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/cluster-image-registry-operator/tree/499e84e43d1b100b5704b30a1b24a1017c9f38b2">cluster-image-registry-operator</a></h3>

<ul>
<li><a target="_blank" href="https://bugzilla.redhat.com/show_bug.cgi?id=2015098">Bug 2015098</a>: Avoid disruptions <a target="_blank" href="https://github.com/openshift/cluster-image-registry-operator/pull/725">#725</a></li>
<li><a target="_blank" href="https://github.com/openshift/cluster-image-registry-operator/compare/46c632b1b428b40e472bed63e19a9bd26be94d34...499e84e43d1b100b5704b30a1b24a1017c9f38b2">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/console/tree/7d748cb6dda5bc040f64f211906b4373bffee21f">console</a></h3>

<ul>
<li><a target="_blank" href="https://bugzilla.redhat.com/show_bug.cgi?id=2001212">Bug 2001212</a>: Notifications is not translated on the top right bar <a target="_blank" href="https://github.com/openshift/console/pull/10040">#10040</a></li>
<li><a target="_blank" href="https://github.com/openshift/console/compare/46aeb866d13002950e6c95f8617691b3a3d868a0...7d748cb6dda5bc040f64f211906b4373bffee21f">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/image-registry/tree/369fd303a1a7e5a2fcc594a63e72dc9cacb4d41f">docker-registry</a></h3>

<ul>
<li><a target="_blank" href="https://bugzilla.redhat.com/show_bug.cgi?id=2012163">Bug 2012163</a>: Supporting mirror authentication during pull through <a target="_blank" href="https://github.com/openshift/image-registry/pull/297">#297</a></li>
<li><a target="_blank" href="https://github.com/openshift/image-registry/compare/55dda00416ba705c5ac34ad82c2cf62e128b4b48...369fd303a1a7e5a2fcc594a63e72dc9cacb4d41f">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/ironic-hardware-inventory-recorder-image/tree/b3ecae8d1c6cd84a8784cf3dd17532797af7b724">ironic-hardware-inventory-recorder</a></h3>

<ul>
<li>Updating ironic-hardware-inventory-recorder-image builder &amp; base images to be consistent with ART <a target="_blank" href="https://github.com/openshift/ironic-hardware-inventory-recorder-image/pull/504">#504</a></li>
<li><a target="_blank" href="https://github.com/openshift/ironic-hardware-inventory-recorder-image/compare/61c4cc7dc99601fe32b239be8923a6ed693908b0...b3ecae8d1c6cd84a8784cf3dd17532797af7b724">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/machine-config-operator/tree/3b46a2229b706cc9aa53e3ed86a407fbe3c5dff4">machine-config-operator</a></h3>

<ul>
<li><a target="_blank" href="https://bugzilla.redhat.com/show_bug.cgi?id=2016275">Bug 2016275</a>: [on-prem] Set coredns bufsize to 512 <a target="_blank" href="https://github.com/openshift/machine-config-operator/pull/2807">#2807</a></li>
<li><a target="_blank" href="https://github.com/openshift/machine-config-operator/compare/6b3b21b4cda00724dd8bc5af2c3a02c426d271ed...3b46a2229b706cc9aa53e3ed86a407fbe3c5dff4">Full changelog</a></li>
</ul>

<h3><a target="_blank" href="https://github.com/openshift/openshift-controller-manager/tree/69a83a3f3c290519692a66fd5ffe89586eb1b4b9">openshift-controller-manager</a></h3>

<ul>
<li><a target="_blank" href="https://bugzilla.redhat.com/show_bug.cgi?id=2006793">Bug 2006793</a>: BC ICT still must check spec last triggered image ID in case BC was last processed when cluster was pre 4.8 <a target="_blank" href="https://github.com/openshift/openshift-controller-manager/pull/206">#206</a></li>
<li><a target="_blank" href="https://github.com/openshift/openshift-controller-manager/compare/2e25328c64ac83e6f25449a6a2507c145352abc9...69a83a3f3c290519692a66fd5ffe89586eb1b4b9">Full changelog</a></li>
</ul>
<hr>
<p><form class="form-inline" method="GET"><a href="/changelog?from=4.8.0-0.nightly-2021-10-20-155651&to=4.8.0-0.nightly-2021-10-27-123103">View changelog in Markdown</a><span>&nbsp;or&nbsp;</span><label for="from">change previous release:&nbsp;</label><select onchange="this.form.submit()" id="from" class="form-control" name="from"><option >4.8.0-0.nightly-2021-10-27-053402</option><option >4.8.0-0.nightly-2021-10-27-034647</option><option >4.8.0-0.nightly-2021-10-26-145352</option><option selected="true">4.8.0-0.nightly-2021-10-20-155651</option><option >4.8.0-0.nightly-2021-10-19-213853</option><option >4.8.0-0.nightly-2021-10-18-115810</option><option >4.8.0-0.nightly-2021-10-16-024756</option><option disabled>───</option><option >4.10.0-0.nightly-2021-10-28-150422</option><option >4.10.0-0.nightly-2021-10-28-112447</option><option >4.10.0-0.nightly-2021-10-28-052206</option><option >4.10.0-0.nightly-2021-10-27-230233</option><option >4.10.0-0.nightly-2021-10-25-190146</option><option >4.10.0-0.nightly-2021-10-25-125739</option><option >4.10.0-0.nightly-2021-10-25-062528</option><option >4.10.0-0.nightly-2021-10-23-225921</option><option disabled>───</option><option >4.10.0-0.ci-2021-10-28-130312</option><option >4.10.0-0.ci-2021-10-28-094851</option><option >4.10.0-0.ci-2021-10-28-062943</option><option >4.10.0-0.ci-2021-10-28-032140</option><option >4.10.0-0.ci-2021-10-28-000124</option><option >4.10.0-0.ci-2021-10-27-204851</option><option >4.10.0-0.ci-2021-10-27-172847</option><option >4.10.0-0.ci-2021-10-27-140539</option><option >4.10.0-0.ci-2021-10-27-104906</option><option >4.10.0-0.ci-2021-10-27-072850</option><option >4.10.0-0.ci-2021-10-27-042149</option><option >4.10.0-0.ci-2021-10-27-010847</option><option >4.10.0-0.ci-2021-10-26-214206</option><option >4.10.0-0.ci-2021-10-26-182849</option><option >4.10.0-0.ci-2021-10-26-150847</option><option >4.10.0-0.ci-2021-10-26-115601</option><option >4.10.0-0.ci-2021-10-26-082859</option><option >4.10.0-0.ci-2021-10-26-051853</option><option >4.10.0-0.ci-2021-10-26-015712</option><option >4.10.0-0.ci-2021-10-25-224425</option><option >4.10.0-0.ci-2021-10-25-193609</option><option >4.10.0-0.ci-2021-10-25-161519</option><option disabled>───</option><option >4.9.0-0.nightly-2021-10-28-150616</option><option >4.9.0-0.nightly-2021-10-28-052726</option><option >4.9.0-0.nightly-2021-10-28-034652</option><option >4.9.0-0.nightly-2021-10-27-202207</option><option >4.9.0-0.nightly-2021-10-27-162129</option><option >4.9.0-0.nightly-2021-10-27-154129</option><option >4.9.0-0.nightly-2021-10-27-091730</option><option >4.9.0-0.nightly-2021-10-27-083730</option><option >4.9.0-0.nightly-2021-10-27-045147</option><option >4.9.0-0.nightly-2021-10-27-041147</option><option >4.9.0-0.nightly-2021-10-26-041726</option><option >4.9.0-0.nightly-2021-10-26-021742</option><option >4.9.0-0.nightly-2021-10-25-231142</option><option >4.9.0-0.nightly-2021-10-25-215312</option><option >4.9.0-0.nightly-2021-10-25-153437</option><option >4.9.0-0.nightly-2021-10-25-093837</option><option >4.9.0-0.nightly-2021-10-25-085837</option><option disabled>───</option><option >4.9.0-0.ci-2021-10-28-143913</option><option >4.9.0-0.ci-2021-10-28-140913</option><option >4.9.0-0.ci-2021-10-28-105124</option><option >4.9.0-0.ci-2021-10-28-085609</option><option >4.9.0-0.ci-2021-10-28-055815</option><option >4.9.0-0.ci-2021-10-28-050748</option><option >4.9.0-0.ci-2021-10-28-015836</option><option >4.9.0-0.ci-2021-10-28-012436</option><option >4.9.0-0.ci-2021-10-27-204104</option><option >4.9.0-0.ci-2021-10-27-181817</option><option >4.9.0-0.ci-2021-10-27-164832</option><option >4.9.0-0.ci-2021-10-27-145053</option><option >4.9.0-0.ci-2021-10-27-133157</option><option >4.9.0-0.ci-2021-10-27-113022</option><option >4.9.0-0.ci-2021-10-27-095323</option><option >4.9.0-0.ci-2021-10-27-070414</option><option >4.9.0-0.ci-2021-10-27-063414</option><option >4.9.0-0.ci-2021-10-27-032803</option><option >4.9.0-0.ci-2021-10-27-025803</option><option >4.9.0-0.ci-2021-10-26-235523</option><option >4.9.0-0.ci-2021-10-26-232425</option><option >4.9.0-0.ci-2021-10-26-203418</option><option >4.9.0-0.ci-2021-10-26-200418</option><option >4.9.0-0.ci-2021-10-26-153231</option><option >4.9.0-0.ci-2021-10-26-134253</option><option >4.9.0-0.ci-2021-10-26-101845</option><option >4.9.0-0.ci-2021-10-26-055548</option><option >4.9.0-0.ci-2021-10-26-030541</option><option >4.9.0-0.ci-2021-10-26-000015</option><option >4.9.0-0.ci-2021-10-25-224204</option><option >4.9.0-0.ci-2021-10-25-164934</option><option >4.9.0-0.ci-2021-10-25-153857</option><option disabled>───</option><option >4.8.0-0.ci-2021-10-28-144649</option><option >4.8.0-0.ci-2021-10-28-095345</option><option >4.8.0-0.ci-2021-10-28-050351</option><option >4.8.0-0.ci-2021-10-28-023800</option><option >4.8.0-0.ci-2021-10-28-001805</option><option >4.8.0-0.ci-2021-10-27-234805</option><option >4.8.0-0.ci-2021-10-27-194726</option><option >4.8.0-0.ci-2021-10-27-180230</option><option >4.8.0-0.ci-2021-10-27-143421</option><option >4.8.0-0.ci-2021-10-27-032703</option><option >4.8.0-0.ci-2021-10-26-233242</option><option disabled>───</option><option >4.7.0-0.nightly-2021-10-27-102551</option><option >4.7.0-0.nightly-2021-10-27-073338</option><option >4.7.0-0.nightly-2021-10-27-045831</option><option >4.7.0-0.nightly-2021-10-21-232712</option><option >4.7.0-0.nightly-2021-10-21-210900</option><option >4.7.0-0.nightly-2021-10-20-164245</option><option >4.7.0-0.nightly-2021-10-20-155546</option><option >4.7.0-0.nightly-2021-10-18-191324</option><option >4.7.0-0.nightly-2021-10-18-161301</option><option disabled>───</option><option >4.7.0-0.ci-2021-10-28-134911</option><option >4.7.0-0.ci-2021-10-28-085818</option><option >4.7.0-0.ci-2021-10-28-041006</option><option >4.7.0-0.ci-2021-10-27-232916</option><option >4.7.0-0.ci-2021-10-27-185429</option><option >4.7.0-0.ci-2021-10-27-161840</option><option >4.7.0-0.ci-2021-10-27-141853</option><option >4.7.0-0.ci-2021-10-27-093518</option><option >4.7.0-0.ci-2021-10-26-234428</option><option >4.7.0-0.ci-2021-10-25-082026</option><option >4.7.0-0.ci-2021-10-25-001839</option><option disabled>───</option><option >4.6.0-0.nightly-2021-10-27-050036</option><option >4.6.0-0.nightly-2021-10-20-163353</option><option >4.6.0-0.nightly-2021-10-20-151628</option><option >4.6.0-0.nightly-2021-10-20-031121</option><option >4.6.0-0.nightly-2021-10-19-094454</option><option disabled>───</option><option >4.6.0-0.ci-2021-10-21-214312</option><option >4.6.0-0.ci-2021-10-21-200301</option><option >4.6.0-0.ci-2021-10-21-193301</option><option >4.6.0-0.ci-2021-10-20-015443</option><option >4.6.0-0.ci-2021-10-19-163440</option><option >4.6.0-0.ci-2021-10-16-162431</option><option >4.6.0-0.ci-2021-10-15-222117</option><option disabled>───</option><option >4.5.0-0.nightly-2021-09-07-164108</option><option >4.5.0-0.nightly-2021-08-31-145050</option><option >4.5.0-0.nightly-2021-08-31-114440</option><option >4.5.0-0.nightly-2021-08-30-102410</option><option >4.5.0-0.nightly-2021-08-11-224754</option><option disabled>───</option><option >4.5.0-0.ci-2021-09-29-191216</option><option >4.5.0-0.ci-2021-09-21-094342</option><option >4.5.0-0.ci-2021-09-13-151813</option><option >4.5.0-0.ci-2021-09-08-114327</option><option >4.5.0-0.ci-2021-09-08-110327</option><option >4.5.0-0.ci-2021-08-03-204850</option><option >4.5.0-0.ci-2021-07-28-155319</option><option >4.5.0-0.ci-2021-06-22-160944</option><option >4.5.0-0.ci-2021-06-16-122653</option><option >4.5.0-0.ci-2021-06-16-114048</option><option >4.5.0-0.ci-2021-06-16-092204</option><option >4.5.0-0.ci-2021-06-15-160917</option><option >4.5.0-0.ci-2021-06-11-154248</option><option >4.5.0-0.ci-2021-06-03-155305</option><option >4.5.0-0.ci-2021-06-02-154548</option><option disabled>───</option><option >4.4.0-0.nightly-2021-03-19-022315</option><option >4.4.0-0.nightly-2021-03-18-142512</option><option >4.4.0-0.nightly-2021-03-17-172050</option><option >4.4.0-0.nightly-2021-03-16-172002</option><option >4.4.0-0.nightly-2021-03-15-164528</option><option disabled>───</option><option >4.4.0-0.ci-2021-09-29-191223</option><option >4.4.0-0.ci-2021-09-13-151639</option><option >4.4.0-0.ci-2021-09-08-110357</option><option >4.4.0-0.ci-2021-09-07-170306</option><option >4.4.0-0.ci-2021-08-19-191840</option><option >4.4.0-0.ci-2021-08-03-204826</option><option >4.4.0-0.ci-2021-05-18-134123</option><option >4.4.0-0.ci-2021-05-13-093804</option><option >4.4.0-0.ci-2021-05-12-175519</option><option >4.4.0-0.ci-2021-03-15-194904</option><option >4.4.0-0.ci-2021-03-10-114123</option><option >4.4.0-0.ci-2021-03-10-071620</option><option >4.4.0-0.ci-2021-02-22-053835</option><option >4.4.0-0.ci-2021-02-11-212925</option><option >4.4.0-0.ci-2021-02-04-021441</option><option disabled>───</option><option >4.3.0-0.nightly-2021-02-23-060813</option><option >4.3.0-0.nightly-2021-02-22-141216</option><option >4.3.0-0.nightly-2021-02-21-141347</option><option >4.3.0-0.nightly-2021-02-20-230558</option><option >4.3.0-0.nightly-2021-02-20-191327</option><option >4.3.0-0.nightly-2020-04-13-190424</option><option >4.3.0-0.nightly-2020-03-23-130439</option><option >4.3.0-0.nightly-2020-03-04-222846</option><option disabled>───</option><option >4.3.0-0.ci-2021-09-07-170212</option><option >4.3.0-0.ci-2021-08-30-194009</option><option >4.3.0-0.ci-2021-08-18-153224</option><option >4.3.0-0.ci-2021-08-18-145222</option><option >4.3.0-0.ci-2021-08-09-190413</option><option >4.3.0-0.ci-2021-02-02-110149</option><option >4.3.0-0.ci-2021-02-01-235009</option><option >4.3.0-0.ci-2021-02-01-210854</option><option >4.3.0-0.ci-2020-12-19-041731</option><option >4.3.0-0.ci-2020-12-12-042426</option><option disabled>───</option><option >4.2.0-0.nightly-2021-02-22-141219</option><option >4.2.0-0.nightly-2021-02-22-022151</option><option >4.2.0-0.nightly-2021-02-21-162759</option><option >4.2.0-0.nightly-2021-02-20-201532</option><option >4.2.0-0.nightly-2021-02-20-161619</option><option disabled>───</option><option >4.2.0-0.ci-2021-10-01-172220</option><option >4.2.0-0.ci-2021-09-23-165347</option><option >4.2.0-0.ci-2021-09-13-151658</option><option >4.2.0-0.ci-2021-09-08-113441</option><option >4.2.0-0.ci-2021-09-08-110441</option><option >4.2.0-0.ci-2021-02-01-235015</option><option >4.2.0-0.ci-2021-01-25-181608</option><option >4.2.0-0.ci-2021-01-25-174608</option><option >4.2.0-0.ci-2021-01-16-051918</option><option >4.2.0-0.ci-2021-01-09-120434</option><option disabled>───</option><option >4.1.0-0.nightly-2020-07-29-210856</option><option >4.1.0-0.nightly-2020-05-28-040321</option><option >4.1.0-0.nightly-2020-05-27-042422</option><option >4.1.0-0.nightly-2020-05-26-035728</option><option >4.1.0-0.nightly-2020-05-25-040303</option><option disabled>───</option><option >4.1.0-0.ci-2021-10-01-172125</option><option >4.1.0-0.ci-2021-09-07-170204</option><option >4.1.0-0.ci-2021-08-09-143907</option><option >4.1.0-0.ci-2021-08-05-021038</option><option >4.1.0-0.ci-2021-08-05-014038</option><option >4.1.0-0.ci-2021-08-04-211610</option><option >4.1.0-0.ci-2021-08-04-204610</option><option >4.1.0-0.ci-2021-08-03-211901</option><option >4.1.0-0.ci-2021-02-04-154232</option><option >4.1.0-0.ci-2021-02-02-193645</option><option >4.1.0-0.ci-2021-02-02-110132</option><option >4.1.0-0.ci-2021-02-01-235021</option><option >4.1.0-0.ci-2021-02-01-211231</option><option disabled>───</option><option >4.9.5</option><option >4.9.4</option><option >4.9.3</option><option >4.9.2</option><option >4.9.1</option><option >4.9.0</option><option >4.9.0-rc.8</option><option >4.9.0-rc.7</option><option >4.9.0-rc.6</option><option >4.9.0-rc.5</option><option >4.9.0-rc.4</option><option >4.9.0-rc.3</option><option >4.9.0-rc.2</option><option >4.9.0-rc.1</option><option >4.9.0-rc.0</option><option >4.9.0-fc.1</option><option >4.9.0-fc.0</option><option >4.8.18</option><option >4.8.17</option><option >4.8.16</option><option >4.8.15</option><option >4.8.14</option><option >4.8.13</option><option >4.8.12</option><option >4.8.11</option><option >4.8.10</option><option >4.8.9</option><option >4.8.8</option><option >4.8.7</option><option >4.8.6</option><option >4.8.5</option><option >4.8.4</option><option >4.8.3</option><option >4.8.2</option><option >4.8.1</option><option >4.8.0</option><option >4.8.0-rc.3</option><option >4.8.0-rc.2</option><option >4.8.0-rc.1</option><option >4.8.0-rc.0</option><option >4.8.0-fc.9</option><option >4.8.0-fc.8</option><option >4.8.0-fc.7</option><option >4.8.0-fc.6</option><option >4.8.0-fc.5</option><option >4.8.0-fc.4</option><option >4.8.0-fc.3</option><option >4.8.0-fc.2</option><option >4.8.0-fc.1</option><option >4.8.0-fc.0</option><option >4.7.36</option><option >4.7.35</option><option >4.7.34</option><option >4.7.33</option><option >4.7.32</option><option >4.7.31</option><option >4.7.30</option><option >4.7.29</option><option >4.7.28</option><option >4.7.27</option><option >4.7.26</option><option >4.7.25</option><option >4.7.24</option><option >4.7.23</option><option >4.7.22</option><option >4.7.21</option><option >4.7.20</option><option >4.7.19</option><option >4.7.18</option><option >4.7.17</option><option >4.7.16</option><option >4.7.15</option><option >4.7.14</option><option >4.7.13</option><option >4.7.12</option><option >4.7.11</option><option >4.7.10</option><option >4.7.9</option><option >4.7.8</option><option >4.7.7</option><option >4.7.6</option><option >4.7.5</option><option >4.7.4</option><option >4.7.3</option><option >4.7.2</option><option >4.7.1</option><option >4.7.0</option><option >4.7.0-rc.3</option><option >4.7.0-rc.2</option><option >4.7.0-rc.1</option><option >4.7.0-rc.0</option><option >4.7.0-fc.5</option><option >4.7.0-fc.4</option><option >4.7.0-fc.3</option><option >4.7.0-fc.2</option><option >4.7.0-fc.1</option><option >4.7.0-fc.0</option><option >4.6.49</option><option >4.6.48</option><option >4.6.47</option><option >4.6.46</option><option >4.6.45</option><option >4.6.44</option><option >4.6.43</option><option >4.6.42</option><option >4.6.41</option><option >4.6.40</option><option >4.6.39</option><option >4.6.38</option><option >4.6.37</option><option >4.6.36</option><option >4.6.35</option><option >4.6.34</option><option >4.6.33</option><option >4.6.32</option><option >4.6.31</option><option >4.6.30</option><option >4.6.29</option><option >4.6.28</option><option >4.6.27</option><option >4.6.26</option><option >4.6.25</option><option >4.6.24</option><option >4.6.23</option><option >4.6.22</option><option >4.6.21</option><option >4.6.20</option><option >4.6.19</option><option >4.6.18</option><option >4.6.17</option><option >4.6.16</option><option >4.6.15</option><option >4.6.14</option><option >4.6.13</option><option >4.6.12</option><option >4.6.11</option><option >4.6.10</option><option >4.6.9</option><option >4.6.8</option><option >4.6.7</option><option >4.6.6</option><option >4.6.5</option><option >4.6.4</option><option >4.6.3</option><option >4.6.2</option><option >4.6.1</option><option >4.6.0</option><option >4.6.0-rc.4</option><option >4.6.0-rc.3</option><option >4.6.0-rc.2</option><option >4.6.0-rc.1</option><option >4.6.0-rc.0</option><option >4.6.0-fc.9</option><option >4.6.0-fc.8</option><option >4.6.0-fc.7</option><option >4.6.0-fc.6</option><option >4.6.0-fc.5</option><option >4.6.0-fc.4</option><option >4.6.0-fc.3</option><option >4.5.41</option><option >4.5.40</option><option >4.5.39</option><option >4.5.38</option><option >4.5.37</option><option >4.5.36</option><option >4.5.35</option><option >4.5.34</option><option >4.5.33</option><option >4.5.32</option><option >4.5.31</option><option >4.5.30</option><option >4.5.29</option><option >4.5.28</option><option >4.5.27</option><option >4.5.26</option><option >4.5.25</option><option >4.5.24</option><option >4.5.23</option><option >4.5.22</option><option >4.5.21</option><option >4.5.20</option><option >4.5.19</option><option >4.5.18</option><option >4.5.17</option><option >4.5.16</option><option >4.5.15</option><option >4.5.14</option><option >4.5.13</option><option >4.5.12</option><option >4.5.11</option><option >4.5.10</option><option >4.5.9</option><option >4.5.8</option><option >4.5.7</option><option >4.5.6</option><option >4.5.5</option><option >4.5.4</option><option >4.5.3</option><option >4.5.2</option><option >4.5.1</option><option >4.5.1-rc.0</option><option >4.5.0</option><option >4.5.0-rc.7</option><option >4.5.0-rc.6</option><option >4.5.0-rc.5</option><option >4.5.0-rc.4</option><option >4.5.0-rc.3</option><option >4.5.0-rc.2</option><option >4.5.0-rc.1</option><option >4.4.33</option><option >4.4.32</option><option >4.4.31</option><option >4.4.30</option><option >4.4.29</option><option >4.4.28</option><option >4.4.27</option><option >4.4.26</option><option >4.4.25</option><option >4.4.24</option><option >4.4.23</option><option >4.4.22</option><option >4.4.21</option><option >4.4.20</option><option >4.4.19</option><option >4.4.18</option><option >4.4.17</option><option >4.4.16</option><option >4.4.15</option><option >4.4.14</option><option >4.4.13</option><option >4.4.12</option><option >4.4.11</option><option >4.4.10</option><option >4.4.9</option><option >4.4.8</option><option >4.4.7</option><option >4.4.6</option><option >4.4.5</option><option >4.4.4</option><option >4.4.3</option><option >4.4.2</option><option >4.4.1</option><option >4.4.0</option><option >4.4.0-rc.13</option><option >4.4.0-rc.12</option><option >4.4.0-rc.11</option><option >4.4.0-rc.10</option><option >4.4.0-rc.9</option><option >4.4.0-rc.8</option><option >4.4.0-rc.7</option><option >4.4.0-rc.6</option><option >4.4.0-rc.5</option><option >4.4.0-rc.4</option><option >4.4.0-rc.3</option><option >4.4.0-rc.2</option><option >4.4.0-rc.1</option><option >4.4.0-rc.0</option><option >4.3.40</option><option >4.3.38</option><option >4.3.35</option><option >4.3.33</option><option >4.3.32</option><option >4.3.31</option><option >4.3.29</option><option >4.3.28</option><option >4.3.27</option><option >4.3.26</option><option >4.3.25</option><option >4.3.24</option><option >4.3.23</option><option >4.3.22</option><option >4.3.21</option><option >4.3.19</option><option >4.3.18</option><option >4.3.17</option><option >4.3.16</option><option >4.3.15</option><option >4.3.14</option><option >4.3.13</option><option >4.3.12</option><option >4.3.11</option><option >4.3.10</option><option >4.3.9</option><option >4.3.8</option><option >4.3.7</option><option >4.3.5</option><option >4.3.3</option><option >4.3.2</option><option >4.3.1</option><option >4.3.0</option><option >4.3.0-rc.3</option><option >4.3.0-rc.2</option><option >4.3.0-rc.1</option><option >4.3.0-rc.0</option><option >4.2.36</option><option >4.2.34</option><option >4.2.33</option><option >4.2.32</option><option >4.2.30</option><option >4.2.29</option><option >4.2.28</option><option >4.2.27</option><option >4.2.26</option><option >4.2.25</option><option >4.2.24</option><option >4.2.23</option><option >4.2.22</option><option >4.2.21</option><option >4.2.20</option><option >4.2.19</option><option >4.2.18</option><option >4.2.16</option><option >4.2.15</option><option >4.2.14</option><option >4.2.13</option><option >4.2.12</option><option >4.2.11</option><option >4.2.10</option><option >4.2.9</option><option >4.2.8</option><option >4.2.7</option><option >4.2.6</option><option >4.2.5</option><option >4.2.4</option><option >4.2.2</option><option >4.2.1</option><option >4.2.0</option><option >4.2.0-rc.5</option><option >4.2.0-rc.4</option><option >4.2.0-rc.3</option><option >4.2.0-rc.2</option><option >4.2.0-rc.1</option><option >4.2.0-rc.0</option><option >4.1.41</option><option >4.1.38</option><option >4.1.37</option><option >4.1.34</option><option >4.1.31</option><option >4.1.30</option><option >4.1.29</option><option >4.1.28</option><option >4.1.27</option><option >4.1.26</option><option >4.1.25</option><option >4.1.24</option><option >4.1.23</option><option >4.1.22</option><option >4.1.21</option><option >4.1.20</option><option >4.1.19</option><option >4.1.18</option><option >4.1.17</option><option >4.1.16</option><option >4.1.15</option><option >4.1.14</option><option >4.1.13</option><option >4.1.12</option><option >4.1.11</option><option >4.1.10</option><option >4.1.9</option><option >4.1.8</option><option >4.1.7</option><option >4.1.6</option><option >4.1.4</option><option >4.1.3</option><option >4.1.2</option><option >4.1.1</option><option >4.1.0</option><option >4.1.0-rc.9</option><option >4.1.0-rc.8</option><option >4.1.0-rc.7</option><option >4.1.0-rc.6</option><option >4.1.0-rc.5</option><option >4.1.0-rc.4</option><option >4.1.0-rc.3</option><option >4.1.0-rc.2</option><option >4.1.0-rc.1</option><option >4.1.0-rc.0</option></select> <input class="btn btn-link" type="submit" value="Compare"></form></p>
<p class="small">Source code for this page located on <a href="https://github.com/openshift/release-controller">github</a></p>
`

func TestChangelog_CoreOSVersion(t *testing.T) {
	tests := []struct {
		name                string
		root                soup.Root
		wantCurrentURL      string
		wantCurrentVersion  string
		wantPreviousURL     string
		wantPreviousVersion string
		wantDiffURL         string
	}{
		{
			name:               "Changelog with no OS upgrade",
			root:               soup.HTMLParse(simpleChangelog),
			wantCurrentURL:     "https://releases-rhcos-art.cloud.privileged.psi.redhat.com/?release=410.84.202110220321-0&stream=releases%2Frhcos-4.10",
			wantCurrentVersion: "410.84.202110220321-0",
		},
		{
			name:                "Changelog with CoreOS upgrade",
			root:                soup.HTMLParse(rejectedWithCoreOSUpgrade),
			wantCurrentURL:      "https://releases-rhcos-art.cloud.privileged.psi.redhat.com/?release=48.84.202110152059-0&stream=releases%2Frhcos-4.8",
			wantCurrentVersion:  "48.84.202110152059-0",
			wantPreviousURL:     "https://releases-rhcos-art.cloud.privileged.psi.redhat.com/?release=48.84.202110270303-0&stream=releases%2Frhcos-4.8",
			wantPreviousVersion: "48.84.202110270303-0",
			wantDiffURL:         "https://releases-rhcos-art.cloud.privileged.psi.redhat.com/diff.html?arch=x86_64&first_release=48.84.202110152059-0&first_stream=releases%2Frhcos-4.8&second_release=48.84.202110270303-0&second_stream=releases%2Frhcos-4.8",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Changelog{
				releaseTag: "test",
				root:       tt.root,
			}
			gotCurrentURL, gotCurrentVersion, gotPreviousURL, gotPreviousVersion, gotDiffURL := c.CoreOSVersion()
			if gotCurrentURL != tt.wantCurrentURL {
				t.Errorf("CoreOSVersion() gotCurrentURL = %v, want %v", gotCurrentURL, tt.wantCurrentURL)
			}
			if gotCurrentVersion != tt.wantCurrentVersion {
				t.Errorf("CoreOSVersion() gotCurrentVersion = %v, want %v", gotCurrentVersion, tt.wantCurrentVersion)
			}
			if gotPreviousURL != tt.wantPreviousURL {
				t.Errorf("CoreOSVersion() gotPreviousURL = %v, want %v", gotPreviousURL, tt.wantPreviousURL)
			}
			if gotPreviousVersion != tt.wantPreviousVersion {
				t.Errorf("CoreOSVersion() gotPreviousVersion = %v, want %v", gotPreviousVersion, tt.wantPreviousVersion)
			}
			if gotDiffURL != tt.wantDiffURL {
				t.Errorf("CoreOSVersion() gotDiffURL = %v, want %v", gotDiffURL, tt.wantDiffURL)
			}
		})
	}
}

func TestChangelog_KubernetesVersion(t *testing.T) {
	tests := []struct {
		name string
		root soup.Root
		want string
	}{
		{
			name: "Changelog has kube version",
			root: soup.HTMLParse(simpleChangelog),
			want: "1.22.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Changelog{
				releaseTag: "test",
				root:       tt.root,
			}
			if got := c.KubernetesVersion(); got != tt.want {
				t.Errorf("KubernetesVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChangelog_PreviousReleaseTag(t *testing.T) {
	tests := []struct {
		name string
		root soup.Root
		want string
	}{
		{
			name: "Changelog fetches previous release tag",
			root: soup.HTMLParse(simpleChangelog),
			want: "4.10.0-0.nightly-2021-10-25-062528",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Changelog{
				releaseTag: "test",
				root:       tt.root,
			}
			if got := c.PreviousReleaseTag(); got != tt.want {
				t.Errorf("PreviousReleaseTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChangelog_PullRequests(t *testing.T) {
	tests := []struct {
		name string
		root soup.Root
		want []jobrunaggregatorapi.ReleasePullRequestRow
	}{
		{
			name: "Extracts pull requests",
			root: soup.HTMLParse(simpleChangelog),
			want: []jobrunaggregatorapi.ReleasePullRequestRow{
				{
					PullRequestID: "583",
					ReleaseTag:    "test",
					Name:          "kuryr-cni, kuryr-controller",
					Description:   "Rebase openshift/kuryr-kubernetes from",
					BugURL:        "",
					URL:           "https://github.com/openshift/kuryr-kubernetes/pull/583",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Changelog{
				releaseTag: "test",
				root:       tt.root,
			}
			if got := c.PullRequests(); !reflect.DeepEqual(got, tt.want) {
				gotJSON, err := json.MarshalIndent(got, "", "    ")
				if err != nil {
					t.Fatal(err)
				}
				wantJSON, err := json.MarshalIndent(tt.want, "", "    ")
				if err != nil {
					t.Fatal(err)
				}

				t.Errorf("PullRequests() = %v, want %v", string(gotJSON), string(wantJSON))
			}
		})
	}
}

func TestChangelog_Repositories(t *testing.T) {
	tests := []struct {
		name string
		root soup.Root
		want []jobrunaggregatorapi.ReleaseRepositoryRow
	}{
		{
			name: "Can list repositories",
			root: soup.HTMLParse(simpleChangelog),
			want: []jobrunaggregatorapi.ReleaseRepositoryRow{
				{
					Name:          "kuryr-cni, kuryr-controller",
					ReleaseTag:    "test",
					Head:          "https://github.com/openshift/kuryr-kubernetes/tree/ae2b8f2f5ae3e4cce74055b44821b502dccf4e27",
					FullChangelog: "https://github.com/openshift/kuryr-kubernetes/compare/ce810161cdae640b7c0cfe7e0b631cf09424270e...ae2b8f2f5ae3e4cce74055b44821b502dccf4e27",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Changelog{
				releaseTag: "test",
				root:       tt.root,
			}
			if got := c.Repositories(); !reflect.DeepEqual(got, tt.want) {
				gotJSON, err := json.MarshalIndent(got, "", "    ")
				if err != nil {
					t.Fatal(err)
				}
				wantJSON, err := json.MarshalIndent(tt.want, "", "    ")
				if err != nil {
					t.Fatal(err)
				}

				t.Errorf("Repositories() = %v, want %v", string(gotJSON), string(wantJSON))

			}
		})
	}
}
