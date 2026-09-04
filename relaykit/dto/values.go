package dto

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type StringValue string

func (s *StringValue) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = StringValue(str)
		return nil
	}

	var raw json.Number
	if err := json.Unmarshal(data, &raw); err == nil {
		*s = StringValue(raw.String())
		return nil
	}

	return json.Unmarshal(data, &str)
}

func (s StringValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}

type IntValue int

func (i *IntValue) UnmarshalJSON(b []byte) error {
	var num json.Number
	if err := json.Unmarshal(b, &num); err == nil {
		if v, err := strconv.Atoi(num.String()); err == nil {
			*i = IntValue(v)
			return nil
		}
		// 容忍浮点数（如 5.0），截断为整数
		if f, err := strconv.ParseFloat(num.String(), 64); err == nil {
			*i = IntValue(int(f))
			return nil
		}
		return fmt.Errorf("invalid number value: %s", num.String())
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if v, err := strconv.Atoi(s); err == nil {
		*i = IntValue(v)
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		*i = IntValue(int(f))
		return nil
	}
	return fmt.Errorf("invalid int value: %q", s)
}

func (i IntValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(i))
}

type FloatValue float64

func (f *FloatValue) UnmarshalJSON(b []byte) error {
	var num json.Number
	if err := json.Unmarshal(b, &num); err == nil {
		if v, err := strconv.ParseFloat(num.String(), 64); err == nil {
			*f = FloatValue(v)
			return nil
		}
		return fmt.Errorf("invalid number value: %s", num.String())
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid float value: %q", s)
	}
	*f = FloatValue(v)
	return nil
}

func (f FloatValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(float64(f))
}

func (f FloatValue) Float64() float64 {
	return float64(f)
}

type BoolValue bool

func (b *BoolValue) UnmarshalJSON(data []byte) error {
	var boolean bool
	if err := json.Unmarshal(data, &boolean); err == nil {
		*b = BoolValue(boolean)
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	if str == "true" {
		*b = BoolValue(true)
	} else if str == "false" {
		*b = BoolValue(false)
	} else {
		return json.Unmarshal(data, &boolean)
	}
	return nil
}
func (b BoolValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(bool(b))
}
