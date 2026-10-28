package imagegen

import "image"

// Traced returns s with a hook that receives each photo picture's crop
// before treatment, for the library attack measurement.
func Traced(s Source, f func(plain *image.RGBA)) Source {
	s.trace = f
	return s
}
