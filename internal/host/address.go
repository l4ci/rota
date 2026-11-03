package host

// HandleAddress is a display-only terminal address. Callers retain the complete
// handle for all operations: an address alone cannot prove Core ownership.
func HandleAddress(handle string) string {
	if v, err := coreDecode(handle); err == nil {
		return v.Address
	}
	return handle
}
