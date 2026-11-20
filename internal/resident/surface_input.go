package resident

// mouseInput and keyInput match the Windows INPUT struct on 64-bit (40 bytes).
// They are defined for every GOOS so the layout can be checked without a
// Windows runner. Only the Windows surface sends them to SendInput.
type mouseInput struct {
	Type uint32
	Mi   mouseUnion
}

type mouseUnion struct {
	Dx          int32
	Dy          int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type keyInput struct {
	Type uint32
	Ki   keyUnion
	_    [8]byte
}

type keyUnion struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}
