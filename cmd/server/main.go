package main

import (
	"flag"

	"k8s.io/klog/v2"

	"podman-k8s-adapter/pkg/server"
)

func main() {
	var (
		port = flag.Int("port", 8443, "Port to serve on")
		host = flag.String("host", "0.0.0.0", "Host to serve on")
		certFile = flag.String("cert-file", "", "Path to TLS certificate file")
		keyFile = flag.String("key-file", "", "Path to TLS private key file")
	)

	klog.InitFlags(nil)
	flag.Parse()

	klog.Infof("Starting Podman Kubernetes API Server...")
	klog.Infof("Listening on %s:%d", *host, *port)

	// Create the API server
	apiServer := server.New(*host, *port)

	// Configure TLS
	if *certFile != "" && *keyFile != "" {
		klog.Infof("Using provided TLS certificate: %s", *certFile)
		if err := apiServer.ListenAndServeTLS(*certFile, *keyFile); err != nil {
			klog.Fatalf("Failed to start HTTPS server: %v", err)
		}
	} else {
		klog.Infof("Generating self-signed certificate...")
		if err := apiServer.ListenAndServeTLSWithSelfSigned(); err != nil {
			klog.Fatalf("Failed to start HTTPS server with self-signed cert: %v", err)
		}
	}
}