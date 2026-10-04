package durationffiapp

import stdtime "time"

func Timeout() stdtime.Duration {
	return 1500 * stdtime.Millisecond
}

func Double(d stdtime.Duration) stdtime.Duration {
	return d * 2
}
