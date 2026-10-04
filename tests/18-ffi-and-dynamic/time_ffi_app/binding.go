package timeffiapp

import stdtime "time"

func CreatedAt() stdtime.Time {
	return stdtime.Unix(1700000000, 250000000).UTC()
}

func PlusSecond(t stdtime.Time) stdtime.Time {
	return t.Add(stdtime.Second)
}
