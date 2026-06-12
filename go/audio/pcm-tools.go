package audio

func GetCoherence(f string) (float64, error) {
	l, r, err := ReadWAV(f)
	if err != nil {
		return 0, err
	}

	rho := Correlation(l, r)
	return rho, nil
}
