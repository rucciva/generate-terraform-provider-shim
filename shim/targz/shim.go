package targz

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"text/template"

	"github.com/pkg/errors"
)

var shimTemplate = `#!/usr/bin/env bash
#
# Generated by generate-terraform-provider-shim: https://github.com/numtide/generate-terraform-provider-shim
#

set -e -o pipefail

plugin_url="{{.DownloadURL}}"
plugin_unpack_dir="${XDG_CACHE_HOME:-$HOME/.cache}/terraform-providers/{{.PluginName}}_v{{.Version}}"
plugin_binary_name="{{.BinaryName}}"
plugin_binary_path="${plugin_unpack_dir}/${plugin_binary_name}"
plugin_binary_sha1="{{.SHA1}}"

if [[ ! -d "${plugin_unpack_dir}" ]]; then
    mkdir -p "${plugin_unpack_dir}"
fi

if [[ -f "${plugin_binary_path}" ]]; then
    current_sha=$(git hash-object "${plugin_binary_path}")
    if [[ $current_sha != "${plugin_binary_sha1}" ]]; then
        rm "${plugin_binary_path}"
    fi
fi

if [[ ! -f "${plugin_binary_path}" ]]; then
    curl -sL "${plugin_url}" | tar xzfC - "${plugin_unpack_dir}"
    chmod 755 "${plugin_binary_path}"
fi

current_sha=$(git hash-object "${plugin_binary_path}")
if [[ $current_sha != "${plugin_binary_sha1}" ]]; then
    echo "plugin binary sha does not match ${current_sha} != ${plugin_binary_sha1}" >&2
    exit 1
fi

exec "${plugin_binary_path}" $@
`

type templateData struct {
	DownloadURL string
	PluginName  string
	Version     string
	BinaryName  string
	SHA1        string
}

func GenerateShim(downloadURL, pluginName, version, binaryName string) (string, error) {

	log.Println("[DEBUG] generating .tar.gz shim:", downloadURL)

	hash, err := getShaOfFileFromURL(downloadURL, binaryName)
	if err != nil {
		return "", errors.Wrapf(err, "while determining sha of binary %s at %s", downloadURL, binaryName)
	}

	t, err := template.New("shim").Parse(shimTemplate)
	if err != nil {
		return "", errors.Wrap(err, "while parsing shim template")
	}

	bb := new(bytes.Buffer)
	err = t.Execute(bb, templateData{
		DownloadURL: downloadURL,
		PluginName:  pluginName,
		Version:     version,
		BinaryName:  binaryName,
		SHA1:        hex.EncodeToString(hash[:]),
	})

	if err != nil {
		return "", errors.Wrap(err, "while rendering template")
	}

	return bb.String(), nil

}

func getShaOfFileFromURL(url, fileName string) ([]byte, error) {
	res, err := http.DefaultClient.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "while getting %s", url)
	}

	defer res.Body.Close()

	gr, err := gzip.NewReader(res.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "while unzipping plugin attachment %s", url)
	}

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, errors.Wrapf(err, "while reading tar archive %s", url)
		}

		if hdr.Name == fileName {
			sh := sha1.New()
			_, err = sh.Write([]byte(fmt.Sprintf("blob %d", hdr.Size)))
			if err != nil {
				return nil, errors.Wrap(err, "while writing to sha")
			}

			_, err = sh.Write([]byte{0})
			if err != nil {
				return nil, errors.Wrap(err, "while writing to sha")
			}

			_, err = io.Copy(sh, tr)
			if err != nil {
				return nil, errors.Wrap(err, "while writing to sha")
			}

			return sh.Sum(nil), nil

		} else {
			_, err = io.Copy(ioutil.Discard, tr)
			if err != nil {
				return nil, errors.Wrap(err, "while writing reading tar archive")
			}
		}
	}

	return nil, errors.Errorf("file %s not found in %s", fileName, url)

}
