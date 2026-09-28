// Package web embeds the server-rendered templates and the static assets, so
// the container ships as a single binary with no files to mount.
package web

import "embed"

// FS holds the HTML templates.
//
//go:embed templates
var FS embed.FS

// Static holds the vendored JavaScript, the stylesheet and the favicon.
// Nothing here is fetched from a CDN at runtime.
//
//go:embed static
var Static embed.FS
