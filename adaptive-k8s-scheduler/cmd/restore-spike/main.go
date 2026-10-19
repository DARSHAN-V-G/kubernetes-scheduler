package main

import (
	"archive/tar"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	tarPath := flag.String("tar", "", "CRIU checkpoint tar archive")
	image := flag.String("image", "adaptive-restore:checkpoint", "OCI image name to import")
	namespace := flag.String("namespace", "ecommerce", "Namespace for the restore probe Pod")
	podName := flag.String("pod", "checkpoint-restore-spike", "Restore probe Pod name")
	k3s := flag.String("k3s", "k3s", "K3s executable")
	flag.Parse()

	if *tarPath == "" {
		fail("--tar is required")
	}
	if err := inspectTar(*tarPath); err != nil {
		fail(err.Error())
	}

	fmt.Printf("checkpoint tar validated: %s\n", *tarPath)
	fmt.Println("attempting containerd image import; a CRIU checkpoint tar is not normally an OCI image")
	if output, err := run(context.Background(), *k3s, "ctr", "images", "import", "--base-name", *image, *tarPath); err != nil {
		fmt.Fprintf(os.Stderr, "restore unsupported: containerd rejected the checkpoint tar as an OCI image: %v\n%s", err, output)
		os.Exit(1)
	}

	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  restartPolicy: Never
  containers:
    - name: restore-probe
      image: %s
      imagePullPolicy: Never
      command: ["/bin/sh", "-c", "echo restore image accepted by containerd; sleep 30"]
`, *podName, *namespace, *image)
	output, err := runWithInput(context.Background(), manifest, *k3s, "kubectl", "apply", "-f", "-")
	if err != nil {
		fail(fmt.Sprintf("containerd accepted the import, but Kubernetes rejected the restore probe: %v\n%s", err, output))
	}
	fmt.Printf("containerd accepted the import and restore probe was created: %s/%s\n", *namespace, *podName)
}

func inspectTar(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open checkpoint tar: %w", err)
	}
	defer file.Close()

	reader := tar.NewReader(file)
	foundDescriptor := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read checkpoint tar: %w", err)
		}
		if strings.HasSuffix(header.Name, "checkpoint/descriptors.json") {
			foundDescriptor = true
		}
	}
	if !foundDescriptor {
		return fmt.Errorf("tar is not a CRIU checkpoint archive: checkpoint/descriptors.json not found")
	}
	return nil
}

func run(ctx context.Context, command string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, command, args...).CombinedOutput()
}

func runWithInput(ctx context.Context, input, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.CombinedOutput()
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "restore spike error:", message)
	os.Exit(1)
}
