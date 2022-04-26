package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"log"
	"math/big"
	"os"
	"sort"
	"sync"
	"time"
)


import (
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KubernetesHostnameLabelName = "kubernetes.io/hostname"

	CiWorkloadLabelName           = "ci-workload"
	CiWorkloadLabelValueBuilds = "builds"
	CiWorkloadLabelValueTests  = "tests"

	CiWorkloadBuildsTaintName = "node-role.kubernetes.io/ci-builds-worker"
	CiWorkloadTestsTaintName = "node-role.kubernetes.io/ci-tests-worker"

	DeploymentNamespace = "ci-scheduling-webhook"
)

var (
	tlsCertFile string
	tlsKeyFile  string
	port       int
	impersonateUser string
	codecs  = serializer.NewCodecFactory(runtime.NewScheme())
	logger  = log.New(os.Stdout, "http: ", log.LstdFlags)

	applyAdditionalReserved    bool
	additionalReservedBuildCPU string
	additionalReservedTestCPU string
	additionalReservedBuildMemory string
	additionalReservedTestMemory string

	mutex = sync.Mutex{}
	buildsNodeNameList = make([]string, 0)
	testsNodeNameList = make([]string, 0)
)

func generateTestCertificate() (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("test private key cannot be created: %v", err.Error())
	}

	// Generate a pem block with the private key
	keyPem := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	tml := x509.Certificate{
		// you can add any attr that you need
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(5, 0, 0),
		// you have to generate a different serial number each execution
		SerialNumber: big.NewInt(123123),
		Subject: pkix.Name{
			CommonName:   "New Name",
			Organization: []string{"New Org."},
		},
		BasicConstraintsValid: true,
	}
	cert, err := x509.CreateCertificate(rand.Reader, &tml, &tml, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("test certificate key cannot be created: %v", err.Error())
	}

	// Generate a pem block with the certificate
	certPem := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	})

	tlsCert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return nil, fmt.Errorf("test certificate could not be loaded: %v", err.Error())
	}

	return &tlsCert, nil
}

func Run(cmd *cobra.Command, args []string) {

	if (tlsCertFile == "" || tlsKeyFile == "") && (tlsCertFile != "" || tlsKeyFile != "") {
		fmt.Println("--tls-cert and --tls-key required must both be specified or both omitted")
		os.Exit(1)
	}

	var cert *tls.Certificate
	var err error
	if tlsCertFile == "" {
		klog.Warning("No cert/key pair specified -- generating test certificate")
		cert, err = generateTestCertificate()
		if err != nil {
			fmt.Printf("Error creating test cert / key: %v", err)
			os.Exit(1)
		}
	} else {
		*cert, err = tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
	}

	kubeConfigPath, kubeConfigPresent := os.LookupEnv("KUBECONFIG")
	ctx := context.TODO()

	kubeConfig := ""
	if kubeConfigPresent {
		kubeConfig = kubeConfigPath
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeConfig)
	if err != nil {
		klog.Errorf("Error initializing client config: %v", err)
		os.Exit(1)
	}

	if impersonateUser != "" {
		config.Impersonate = rest.ImpersonationConfig{
			UserName: impersonateUser,
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Errorf("Error initializing client set: %v", err)
		os.Exit(1)
	}

	initNodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error initializing node watcher: %v", err)
		os.Exit(1)
	}

	for _, node := range initNodeList.Items {
		nodePresent(&node, true)
	}

	watcher, err := clientset.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error initializing node watcher: %v", err)
		os.Exit(1)
	}

	go func() {
		for event := range watcher.ResultChan() {
			node := event.Object.(*corev1.Node)
			switch event.Type {
			case watch.Added:
				nodePresent(node, true)
			case watch.Deleted:
				nodePresent(node, false)
			}
		}
	}()

	if applyAdditionalReserved {

		buildDaemonSet := systemReservingDaemonset(CiWorkloadLabelValueBuilds, additionalReservedBuildCPU, additionalReservedBuildMemory)
		err := createOrUpdateDaemonSet(ctx, clientset, buildDaemonSet)
		if err != nil {
			klog.Errorf("Unable to create daemonset for additional system reserved: %v", err)
			os.Exit(1)
		}

		testDaemonSet := systemReservingDaemonset(CiWorkloadLabelValueTests, additionalReservedTestCPU, additionalReservedBuildMemory)
		err = createOrUpdateDaemonSet(ctx, clientset, testDaemonSet)
		if err != nil {
			klog.Errorf("Unable to create daemonset for additional system reserved: %v", err)
			os.Exit(1)
		}
	}

	runWebhookServer(cert)
}

var rootCmd = &cobra.Command{
	Use:   "ci-scheduling-webhook",
	Short: "Improves cost-efficiency when scheduling OpenShift's CI workloads",
	Long: `Improves cost-efficiency when scheduling OpenShift's CI workloads.

Example:
$ ci-scheduling-webhook --tls-cert <tls_cert> --tls-key <tls_key> --port <port>`,
	Run: Run,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func removeFromHostnames(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			s = append(s[:i], s[i+1:]...)
		}
	}
	sort.Strings(s)
	return s
}

func addToHostnames(s []string, r string) []string {
	s = removeFromHostnames(s, r) // eliminate any potential for duplicates
	s = append(s, r)
	sort.Strings(s)
	return s
}

func nodePresent(node *corev1.Node, exists bool) {
	mutex.Lock()
	defer mutex.Unlock()

	if hostname, ok := node.Labels[KubernetesHostnameLabelName]; ok {
		if val, ok := node.Labels[CiWorkloadLabelName]; ok {
			var modification func([]string, string) []string
			if exists {
				klog.InfoS("Registering node in cache", "hostname", hostname, CiWorkloadLabelName, val)
				modification = addToHostnames
			} else {
				klog.InfoS("Removing node from cache", "hostname", hostname, CiWorkloadLabelName, val)
				modification = removeFromHostnames
			}
			switch val {
			case CiWorkloadLabelValueBuilds:
				buildsNodeNameList = modification(buildsNodeNameList, hostname)
			case CiWorkloadLabelValueTests:
				testsNodeNameList = modification(testsNodeNameList, hostname)
			}
		}
	} else {
		klog.Warningf("Node %v did not contain label %v; incompatible API change?", node.Name, KubernetesHostnameLabelName)
		return
	}
}


func init() {
	rootCmd.Flags().StringVar(&tlsCertFile, "tls-cert", "", "Certificate for TLS")
	rootCmd.Flags().StringVar(&tlsKeyFile, "tls-key", "", "Private key file for TLS")
	rootCmd.Flags().IntVar(&port, "port", 443, "Port to listen on for HTTPS traffic")
	rootCmd.Flags().StringVar(&impersonateUser, "as", "", "Impersonate a user, like system:admin")

	rootCmd.Flags().BoolVar(&applyAdditionalReserved, "apply-additional-reserved", false, "Create or update daemonsets to reserve additional system memory")
	rootCmd.Flags().StringVar(&additionalReservedBuildCPU, "reserve-system-cpu-builds", "100m", "Additional cores to reserve on build workload nodes using daemonset")
	rootCmd.Flags().StringVar(&additionalReservedBuildMemory, "reserve-system-memory-builds", "200Mi", "Additional bytes to reserve on build workload nodes using daemonset")
	rootCmd.Flags().StringVar(&additionalReservedTestCPU, "reserve-system-cpu-tests", "100m", "Additional cores to reserve on test workload nodes using daemonset")
	rootCmd.Flags().StringVar(&additionalReservedTestMemory, "reserve-system-memory-tests", "200Mi", "Additional bytes to reserve on test workload nodes using daemonset")
}

func admissionReviewFromRequest(r *http.Request, deserializer runtime.Decoder) (*admissionv1.AdmissionReview, error) {
	// Validate that the incoming content type is correct.
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("expected application/json content-type")
	}

	// Get the body data, which will be the AdmissionReview
	// content for the request.
	var body []byte
	if r.Body != nil {
		requestData, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		body = requestData
	}

	// Decode the request body into
	admissionReviewRequest := &admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, admissionReviewRequest); err != nil {
		return nil, err
	}

	return admissionReviewRequest, nil
}

func mutatePod(w http.ResponseWriter, r *http.Request) {
	logger.Printf("received message on mutate")

	deserializer := codecs.UniversalDeserializer()

	// Parse the AdmissionReview from the http request.
	admissionReviewRequest, err := admissionReviewFromRequest(r, deserializer)
	if err != nil {
		msg := fmt.Sprintf("error getting admission review from request: %v", err)
		logger.Printf(msg)
		w.WriteHeader(400)
		w.Write([]byte(msg))
		return
	}

	// Do server-side validation that we are only dealing with a pod resource. This
	// should also be part of the MutatingWebhookConfiguration in the cluster, but
	// we should verify here before continuing.
	podResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	if admissionReviewRequest.Request.Resource != podResource {
		msg := fmt.Sprintf("did not receive pod, got %s", admissionReviewRequest.Request.Resource.Resource)
		logger.Printf(msg)
		w.WriteHeader(400)
		w.Write([]byte(msg))
		return
	}

	// Decode the pod from the AdmissionReview.
	rawRequest := admissionReviewRequest.Request.Object.Raw
	pod := corev1.Pod{}
	if _, _, err := deserializer.Decode(rawRequest, nil, &pod); err != nil {
		msg := fmt.Sprintf("error decoding raw pod: %v", err)
		logger.Printf(msg)
		w.WriteHeader(500)
		w.Write([]byte(msg))
		return
	}

	// Create a response that will add a label to the pod if it does
	// not already have a label with the key of "hello". In this case
	// it does not matter what the value is, as long as the key exists.
	admissionResponse := &admissionv1.AdmissionResponse{}
	var patch string
	patchType := v1.PatchTypeJSONPatch
	if _, ok := pod.Labels["hello"]; !ok {
		patch = `[{"op":"add","path":"/metadata/labels","value":{"hello":"world"}}]`
	}

	admissionResponse.Allowed = true
	if patch != "" {
		admissionResponse.PatchType = &patchType
		admissionResponse.Patch = []byte(patch)
	}

	// Construct the response, which is just another AdmissionReview.
	var admissionReviewResponse admissionv1.AdmissionReview
	admissionReviewResponse.Response = admissionResponse
	admissionReviewResponse.SetGroupVersionKind(admissionReviewRequest.GroupVersionKind())
	admissionReviewResponse.Response.UID = admissionReviewRequest.Request.UID

	resp, err := json.Marshal(admissionReviewResponse)
	if err != nil {
		msg := fmt.Sprintf("error marshalling response json: %v", err)
		logger.Printf(msg)
		w.WriteHeader(500)
		w.Write([]byte(msg))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func runWebhookServer(cert *tls.Certificate) {
	fmt.Println("Starting webhook server")
	http.HandleFunc("/mutate", mutatePod)
	server := http.Server{
		Addr: fmt.Sprintf(":%d", port),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{*cert},
		},
		ErrorLog: logger,
	}

	if err := server.ListenAndServeTLS("", ""); err != nil {
		panic(err)
	}
}

func main() {
	Execute()
}