// Package version porte la version unique de vps et de prj.
// AD-5 : vps embarque les binaires prj Linux de sa propre version.
package version

import "muxfleet/internal/domain"

const (
	// Version est la version commune de vps et de prj.
	Version = "0.1.0"

	// SchemaVersion est la version des documents JSON échangés (AD-19).
	SchemaVersion = "1.0"

	// PrjMarker est inscrit tel quel dans le binaire prj ; vps le lit avant toute poussée (R9).
	PrjMarker = domain.PrjVersionMarkerPrefix + Version + domain.PrjVersionMarkerSuffix
)
