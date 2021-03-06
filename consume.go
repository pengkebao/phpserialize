package phpserialize

import (
	"errors"
	"reflect"
	"strconv"
)

// The internal consume functions work as the parser/lexer when reading
// individual items off the serialized stream.

// consumeStringUntilByte will return a string that includes all characters
// after the given offset, but only up until (and not including) a found byte.
//
// This function will only work with a plain, non-encoded series of bytes. It
// should not be used to capture anything other that ASCII data that is
// terminated by a single byte.
func consumeStringUntilByte(data []byte, lookingFor byte, offset int) (s string, newOffset int) {
	newOffset = findByte(data, lookingFor, offset)
	if newOffset < 0 {
		return "", -1
	}

	s = string(data[offset:newOffset])
	return
}

func consumeInt(data []byte, offset int) (int64, int, error) {
	if !checkType(data, 'i', offset) {
		return 0, -1, errors.New("not an integer")
	}

	alphaNumber, newOffset := consumeStringUntilByte(data, ';', offset+2)
	i, err := strconv.Atoi(alphaNumber)
	if err != nil {
		return 0, -1, err
	}

	// The +1 is to skip over the final ';'
	return int64(i), newOffset + 1, nil
}

func consumeFloat(data []byte, offset int) (float64, int, error) {
	if !checkType(data, 'd', offset) {
		return 0, -1, errors.New("not a float")
	}

	alphaNumber, newOffset := consumeStringUntilByte(data, ';', offset+2)
	v, err := strconv.ParseFloat(alphaNumber, 64)
	if err != nil {
		return 0, -1, err
	}

	return v, newOffset + 1, nil
}

func consumeString(data []byte, offset int) (string, int, error) {
	if !checkType(data, 's', offset) {
		return "", -1, errors.New("not a string")
	}

	return consumeStringRealPart(data, offset+2)
}

// consumeIntPart will consume an integer followed by and including a colon.
// This is used in many places to describe the number of elements or an upcoming
// length.
func consumeIntPart(data []byte, offset int) (int, int, error) {
	rawValue, newOffset := consumeStringUntilByte(data, ':', offset)
	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return 0, -1, err
	}

	// The +1 is to skip over the ':'
	return value, newOffset + 1, nil
}

func consumeStringRealPart(data []byte, offset int) (string, int, error) {
	length, newOffset, err := consumeIntPart(data, offset)
	if err != nil {
		return "", -1, err
	}

	// Skip over the '"' at the start of the string. I'm not sure why they
	// decided to wrap the string in double quotes since it's totally
	// redundant.
	offset = newOffset + 1

	s := DecodePHPString(data)

	// The +2 is to skip over the final '";'
	return s[offset : offset+length], offset + length + 2, nil
}

func consumeNil(data []byte, offset int) (interface{}, int, error) {
	if !checkType(data, 'N', offset) {
		return nil, -1, errors.New("not null")
	}

	return nil, offset + 2, nil
}

func consumeBool(data []byte, offset int) (bool, int, error) {
	if !checkType(data, 'b', offset) {
		return false, -1, errors.New("not a boolean")
	}

	return data[offset+2] == '1', offset + 4, nil
}

func consumeObject(data []byte, offset int, v interface{}) (int, error) {
	if !checkType(data, 'O', offset) {
		return -1, errors.New("not an object")
	}

	// Read the class name. The class name follows the same format as a
	// string. We could just ignore the length and hope that no class name
	// ever had a non-ascii characters in it, but this is safer - and
	// probably easier.
	_, offset, err := consumeStringRealPart(data, offset+2)
	if err != nil {
		return -1, err
	}

	// Read the number of elements in the object.
	length, offset, err := consumeIntPart(data, offset)
	if err != nil {
		return -1, err
	}

	// Skip over the '{'
	offset++

	// Read the elements
	for i := 0; i < length; i++ {
		var key string
		var value interface{}

		// The key should always be a string. I am not completely sure
		// about this.
		key, offset, err = consumeString(data, offset)
		if err != nil {
			return -1, err
		}

		// Check the the key exists in the struct, otherwise the value
		// is discarded.
		//
		// We need to uppercase the first letter for compatibility.
		// The Marshal() function does the opposite of this.
		field := reflect.ValueOf(v).Elem().
			FieldByName(upperCaseFirstLetter(key))

		// If the next item is an object we can't simply consume it,
		// rather we send the reflect.Value back through consumeObject
		// so the recursion can be handled correctly.
		if data[offset] == 'O' {
			var subV interface{}

			if field.IsValid() {
				subV = field.Addr().Interface()
			} else {
				// If the field (key) does not exist on the
				// struct we pass through a dummy object that no
				// keys so that all of the values are discarded
				// but the parser can continue to operate
				// easily.
				subV = &dummyObject{}
			}

			offset, err = consumeObject(data, offset, subV)
			if err != nil {
				return -1, err
			}
		} else {
			value, offset, err = consumeNext(data, offset)
			if err != nil {
				return -1, err
			}

			if field.IsValid() {
				setField(field, reflect.ValueOf(value))
			}
		}
	}

	// The +1 is for the final '}'
	return offset + 1, nil
}

func setField(field, value reflect.Value) {
	switch field.Type().Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64:
		field.SetInt(value.Int())

	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(value.Uint())

	case reflect.Float32, reflect.Float64:
		field.SetFloat(value.Float())

	default:
		field.Set(value)
	}
}

func consumeNext(data []byte, offset int) (interface{}, int, error) {
	if offset >= len(data) {
		return nil, -1, errors.New("corrupt")
	}

	switch data[offset] {
	case 'b':
		return consumeBool(data, offset)
	case 'd':
		return consumeFloat(data, offset)
	case 'i':
		return consumeInt(data, offset)
	case 's':
		return consumeString(data, offset)
	case 'N':
		return consumeNil(data, offset)
	}

	return nil, -1, errors.New("can not consume type: " +
		string(data[offset:]))
}
