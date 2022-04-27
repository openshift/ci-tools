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
	"net/http"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	CiWorkloadLabelName           = "ci-workload"
	CiWorkloadNamespaceLabelName           = "ci-workload-namespace"
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

	shrinkTestCPU float32
	shrinkBuildCPU float32

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

	if val, ok := node.Labels[CiWorkloadLabelName]; ok {
		var modification func([]string, string) []string
		if exists {
			klog.InfoS("Registering node in cache", "hostname", node.Name, CiWorkloadLabelName, val)
			modification = addToHostnames
		} else {
			klog.InfoS("Removing node from cache", "hostname", node.Name, CiWorkloadLabelName, val)
			modification = removeFromHostnames
		}
		switch val {
		case CiWorkloadLabelValueBuilds:
			buildsNodeNameList = modification(buildsNodeNameList, node.Name)
		case CiWorkloadLabelValueTests:
			testsNodeNameList = modification(testsNodeNameList, node.Name)
		}
	}

}


func init() {
	rootCmd.Flags().StringVar(&tlsCertFile, "tls-cert", "", "Certificate for TLS")
	rootCmd.Flags().StringVar(&tlsKeyFile, "tls-key", "", "Private key file for TLS")
	rootCmd.Flags().IntVar(&port, "port", 443, "Port to listen on for HTTPS traffic")
	rootCmd.Flags().StringVar(&impersonateUser, "as", "", "Impersonate a user, like system:admin")

	rootCmd.Flags().Float32Var(&shrinkTestCPU, "shrink-cpu-requests-tests", 1.0, "Multiply test workload CPU requests by this factor")
	rootCmd.Flags().Float32Var(&shrinkBuildCPU, "shrink-cpu-requests-builds", 1.0, "Multiply build workload CPU requests by this factor")

	rootCmd.Flags().BoolVar(&applyAdditionalReserved, "apply-additional-reserved", false, "Create or update daemonsets to reserve additional system memory")
	rootCmd.Flags().StringVar(&additionalReservedBuildCPU, "reserve-system-cpu-builds", "100m", "Additional cores to reserve on build workload nodes using daemonset")
	rootCmd.Flags().StringVar(&additionalReservedBuildMemory, "reserve-system-memory-builds", "200Mi", "Additional bytes to reserve on build workload nodes using daemonset")
	rootCmd.Flags().StringVar(&additionalReservedTestCPU, "reserve-system-cpu-tests", "100m", "Additional cores to reserve on test workload nodes using daemonset")
	rootCmd.Flags().StringVar(&additionalReservedTestMemory, "reserve-system-memory-tests", "200Mi", "Additional bytes to reserve on test workload nodes using daemonset")
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