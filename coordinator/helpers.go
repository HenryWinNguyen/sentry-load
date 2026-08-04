package main

import "strconv"

func intField(v interface{}) int {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

func floatField(v interface{}) float64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func strField(v interface{}) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
