package geo

import (
	"bytes"
	"fmt"

	"github.com/umaneldi/douceville/internal/models"
)

// PolygonWKT construit la représentation WKT POLYGON((...)) de l'anneau de
// l'isochrone, utilisable avec ST_GeomFromEWKT côté PostGIS.
func PolygonWKT(iso models.Isochrone) string {
	return RingWKT(iso.Geometry)
}

// RingWKT construit la représentation WKT POLYGON((...)) d'un anneau.
func RingWKT(ring [][2]float64) string {
	var b bytes.Buffer
	b.WriteString("POLYGON((")
	for i, pt := range ring {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%f %f", pt[0], pt[1])
	}
	b.WriteString("))")
	return b.String()
}
