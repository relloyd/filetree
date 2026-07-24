// Package icons maps file names and extensions to Nerd Font glyphs and
// colours. The bulk of the table is generated from nvim-web-devicons (see
// table.go); devops-specific entries missing upstream are added in init.
package icons

import (
	"path/filepath"
	"strings"
)

// Icon is a single Nerd Font glyph with its conventional hex colour.
type Icon struct {
	Glyph string
	Color string
}

var (
	DefaultFile = Icon{Glyph: "\uF15B", Color: "#6D8086"} // nf-fa-file
	ClosedDir   = Icon{Glyph: "\uF07B", Color: "#569CD6"} // nf-fa-folder
	OpenDir     = Icon{Glyph: "\uF07C", Color: "#569CD6"} // nf-fa-folder_open
)

func init() {
	// Terraform/Terragrunt/HCL: reuse the terraform glyph with HashiCorp hues.
	if tf, ok := byExtension["tf"]; ok {
		byExtension["hcl"] = Icon{Glyph: tf.Glyph, Color: "#7B42BC"}
		byExtension["tfstate"] = Icon{Glyph: tf.Glyph, Color: "#6D8086"}
		byFilename["terragrunt.hcl"] = Icon{Glyph: tf.Glyph, Color: "#5F43E9"}
		byFilename[".terraform-version"] = Icon{Glyph: tf.Glyph, Color: "#5F43E9"}
	}
	// Helm and Kustomize: yaml glyph with helm/k8s brand colours.
	if y, ok := byExtension["yaml"]; ok {
		helm := Icon{Glyph: y.Glyph, Color: "#277A9F"}
		byFilename["chart.yaml"] = helm
		byFilename["values.yaml"] = helm
		byFilename["helmfile.yaml"] = helm
		byFilename["kustomization.yaml"] = Icon{Glyph: y.Glyph, Color: "#326CE5"}
	}
	if gear, ok := byExtension["conf"]; ok {
		byExtension["proto"] = Icon{Glyph: gear.Glyph, Color: "#519ABA"}
	}
	if key, ok := byExtension["pub"]; ok {
		for _, ext := range []string{"pem", "crt", "cer", "key", "p12"} {
			byExtension[ext] = key
		}
	}
}

// For returns the icon for a directory entry. Directories get folder glyphs
// (git repos keep the default folder; special-casing is left to the caller).
func For(name string, isDir, expanded bool) Icon {
	if isDir {
		if expanded {
			return OpenDir
		}
		return ClosedDir
	}
	lower := strings.ToLower(name)
	if ic, ok := byFilename[lower]; ok {
		return ic
	}
	// .env.local, .env.production, etc.
	if strings.HasPrefix(lower, ".env.") {
		if ic, ok := byFilename[".env"]; ok {
			return ic
		}
	}
	ext := strings.TrimPrefix(filepath.Ext(lower), ".")
	if ic, ok := byExtension[ext]; ok {
		return ic
	}
	return DefaultFile
}
