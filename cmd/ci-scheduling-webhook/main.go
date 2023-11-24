package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const (
	CiWorkloadLabelName                 = "ci-workload"
	CiWorkloadPreferNoScheduleTaintName = "ci-workload-avoid"
	CiWorkloadPreferNoExecuteTaintName  = "ci-workload-evict"
	CiWorkloadNamespaceLabelName        = "ci-workload-namespace"
)

var (
	tlsCertFile     string
	tlsKeyFile      string
	port            int
	impersonateUser string
	codecs          = serializer.NewCodecFactory(runtime.NewScheme())
	logger          = log.New(os.Stdout, "http: ", log.LstdFlags)

	shrinkTestCPU  float32
	shrinkBuildCPU float32
	prioritization Prioritization
)

func generateTestCertificate() (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("test private key cannot be created: %w", err)
	}

	// Generate a pem block with the private key
	keyPem := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	tml := x509.Certificate{
		// you can add any attr that you need
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(5, 0, 0),
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
		return nil, fmt.Errorf("test certificate key cannot be created: %w", err)
	}

	// Generate a pem block with the certificate
	certPem := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	})

	tlsCert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return nil, fmt.Errorf("test certificate could not be loaded: %w", err)
	}

	return &tlsCert, nil
}

func Run(_ *cobra.Command, _ []string) {

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
		certP, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
		if err != nil {
			fmt.Printf("Error loading tls files from file system: %v", err)
			os.Exit(1)
		}
		cert = &certP
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

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Errorf("Error initializing kubernetes client set: %v", err)
		os.Exit(1)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Errorf("Error initializing dynamic client: %v", err)
		os.Exit(1)
	}

	prioritization = Prioritization{
		context:       ctx,
		k8sClientSet:  clientSet,
		dynamicClient: dynamicClient,
	}
	err = prioritization.initializePrioritization()
	if err != nil {
		klog.Errorf("Error initializing node prioritization processes: %v", err)
		os.Exit(1)
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

func init() {
	rootCmd.Flags().StringVar(&tlsCertFile, "tls-cert", "", "Certificate for TLS")
	rootCmd.Flags().StringVar(&tlsKeyFile, "tls-key", "", "Private key file for TLS")
	rootCmd.Flags().IntVar(&port, "port", 443, "Port to listen on for HTTPS traffic")
	rootCmd.Flags().StringVar(&impersonateUser, "as", "", "Impersonate a user, like system:admin")

	rootCmd.Flags().Float32Var(&shrinkTestCPU, "shrink-cpu-requests-tests", 1.0, "Multiply test workload CPU requests by this factor")
	rootCmd.Flags().Float32Var(&shrinkBuildCPU, "shrink-cpu-requests-builds", 1.0, "Multiply build workload CPU requests by this factor")
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
