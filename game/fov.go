package game

import "math"

// castFOV fires NumRays across FOVDegrees centred on camAngle and returns
// each ray's hit list, left to right.
func castFOV(m *Map, ox, oy, camAngle float64) [][]rayHit {
	fov := float64(FOVDegrees) * math.Pi / 180
	start := camAngle - fov/2
	step := fov / float64(NumRays-1)

	hits := make([][]rayHit, NumRays)
	for i := range hits {
		ang := start + float64(i)*step
		hits[i] = castRayDDA(m, ox, oy, math.Cos(ang), math.Sin(ang))
	}
	return hits
}
