package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"text/template"

	"github.com/paularlott/zcode-proxy/build"
)

const formulaTemplate = `class ZcodeProxy < Formula
	desc "TCP proxy with automatic failover, relaying a local listen address to an ordered list of upstream backends"
	homepage "https://github.com/paularlott/zcode-proxy"
	license "MIT"
	version "{{ .Version }}"
	if OS.mac?
		if Hardware::CPU.arm?
			url "https://github.com/paularlott/zcode-proxy/releases/download/v#{version}/zcode-proxy_darwin_arm64.zip"
			sha256 "{{ .Checksum.DarwinArm64 }}"
		else
			url "https://github.com/paularlott/zcode-proxy/releases/download/v#{version}/zcode-proxy_darwin_amd64.zip"
			sha256 "{{ .Checksum.DarwinAmd64 }}"
		end
	elsif OS.linux?
		if Hardware::CPU.arm?
			url "https://github.com/paularlott/zcode-proxy/releases/download/v#{version}/zcode-proxy_linux_arm64.zip"
			sha256 "{{ .Checksum.LinuxArm64 }}"
		else
			url "https://github.com/paularlott/zcode-proxy/releases/download/v#{version}/zcode-proxy_linux_amd64.zip"
			sha256 "{{ .Checksum.LinuxAmd64 }}"
		end
	end

	def install
		bin.install "zcode-proxy"
	end
end
`

func main() {
	data := struct {
		Version  string
		Checksum struct {
			DarwinArm64 string
			DarwinAmd64 string
			LinuxArm64  string
			LinuxAmd64  string
		}
	}{
		Version: build.Version,
	}

	files := map[string]*string{
		"dist/zcode-proxy_darwin_amd64.zip": &data.Checksum.DarwinAmd64,
		"dist/zcode-proxy_darwin_arm64.zip": &data.Checksum.DarwinArm64,
		"dist/zcode-proxy_linux_amd64.zip":  &data.Checksum.LinuxAmd64,
		"dist/zcode-proxy_linux_arm64.zip":  &data.Checksum.LinuxArm64,
	}

	for file, checksum := range files {
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening file %s: %v\n", file, err)
			os.Exit(1)
		}

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			fmt.Fprintf(os.Stderr, "Error calculating checksum for file %s: %v\n", file, err)
			f.Close()
			os.Exit(1)
		}

		*checksum = fmt.Sprintf("%x", h.Sum(nil))
		f.Close()
	}

	tmpl, err := template.New("formula").Parse(formulaTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating template: %v\n", err)
		os.Exit(1)
	}

	if err := tmpl.Execute(os.Stdout, data); err != nil {
		fmt.Fprintf(os.Stderr, "Error executing template: %v\n", err)
		os.Exit(1)
	}
}
