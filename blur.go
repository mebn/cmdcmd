package main

// BlurShape is a rounded rectangle, in window points from the top left, that
// gets the frosted backdrop.
type BlurShape struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Radius float64 `json:"radius"`
}

// SetBlur blurs the desktop behind the given shapes (the cards the frontend
// draws) and nowhere else. It does nothing where the platform can't.
func (l *Launcher) SetBlur(shapes []BlurShape) {
	if l.window != nil {
		setBlur(l.window, shapes)
	}
}
