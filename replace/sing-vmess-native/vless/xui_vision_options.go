package vless

import "fmt"

// ValidateVisionTestseed bounds padding to the uint16 Vision wire format.
func ValidateVisionTestseed(values []uint32) ([4]uint32, error) {
	seed := [4]uint32{900, 500, 900, 256}
	if len(values) == 0 {
		return seed, nil
	}
	if len(values) != 4 {
		return seed, fmt.Errorf("testseed must contain exactly four integers")
	}
	copy(seed[:], values)
	if seed[0] == 0 || seed[1] == 0 || seed[3] == 0 || seed[2] < seed[0]-1 || uint64(seed[1])+uint64(seed[2])-1 > 65535 || seed[3] > 65536 {
		return seed, fmt.Errorf("testseed produces invalid Vision padding")
	}
	return seed, nil
}
func (c *Client) SetVisionTestseed(values []uint32) error {
	seed, err := ValidateVisionTestseed(values)
	if err == nil {
		c.testseed = seed
	}
	return err
}
func (s *Service[T]) SetVisionTestseed(values []uint32) error {
	seed, err := ValidateVisionTestseed(values)
	if err == nil {
		s.testseed = seed
	}
	return err
}
