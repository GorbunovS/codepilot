package tests

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// TensorProto represents a simplified version of ONNX TensorProto.
type TensorProto struct {
	Dims     []int64
	DataType int32
	RawData  []byte
}

// LoadTensorProto loads a TensorProto from a .pb file
// Note: This is a simplified implementation that reads the most common fields
// For production use, consider using a full protobuf library
func LoadTensorProto(path string) (*TensorProto, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read pb file: %w", err)
	}

	tensor := &TensorProto{}

	// Parse the protobuf wire format
	// This is a basic parser for the essential fields we need
	// Field numbers in TensorProto:
	//   1: dims (repeated int64)
	//   2: data_type (int32)
	//   9: raw_data (bytes)

	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		// Read field tag (varint)
		tag, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			break
		}
		offset += n

		fieldNumber := tag >> 3
		wireType := tag & 0x7

		switch fieldNumber {
		case 1: // dims (repeated int64)
			if wireType == 0 { // varint
				val, n := binary.Uvarint(data[offset:])
				if n <= 0 {
					return nil, fmt.Errorf("failed to parse dims")
				}
				tensor.Dims = append(tensor.Dims, int64(val))
				offset += n
			}

		case 2: // data_type (int32)
			if wireType == 0 { // varint
				val, n := binary.Uvarint(data[offset:])
				if n <= 0 {
					return nil, fmt.Errorf("failed to parse data_type")
				}
				tensor.DataType = int32(val)
				offset += n
			}

		case 9: // raw_data
			if wireType == 2 { // length-delimited
				length, n := binary.Uvarint(data[offset:])
				if n <= 0 {
					return nil, fmt.Errorf("failed to parse raw_data length")
				}
				offset += n

				if offset+int(length) > len(data) {
					return nil, fmt.Errorf("raw_data length exceeds buffer")
				}

				tensor.RawData = data[offset : offset+int(length)]
				offset += int(length)
			}

		default:
			// Skip unknown fields
			if wireType == 0 { // varint
				_, n := binary.Uvarint(data[offset:])
				if n <= 0 {
					break
				}
				offset += n
			} else if wireType == 2 { // length-delimited
				length, n := binary.Uvarint(data[offset:])
				if n <= 0 {
					break
				}
				offset += n
				if offset+int(length) <= len(data) {
					offset += int(length)
				}
			} else if wireType == 5 { // 32-bit
				offset += 4
			} else if wireType == 1 { // 64-bit
				offset += 8
			}
		}
	}

	return tensor, nil
}

// ToFloat32 converts tensor data to float32 slice
func (t *TensorProto) ToFloat32() ([]float32, error) {
	if t.DataType != 1 { // ONNX FLOAT = 1
		return nil, fmt.Errorf("tensor is not float32 type (got type %d)", t.DataType)
	}

	if len(t.RawData)%4 != 0 {
		return nil, fmt.Errorf("invalid raw_data length for float32")
	}

	count := len(t.RawData) / 4
	result := make([]float32, count)

	for i := range count {
		bits := binary.LittleEndian.Uint32(t.RawData[i*4 : (i+1)*4])
		result[i] = math.Float32frombits(bits)
	}

	return result, nil
}

// ToInt64 converts tensor data to int64 slice
func (t *TensorProto) ToInt64() ([]int64, error) {
	if t.DataType != 7 { // ONNX INT64 = 7
		return nil, fmt.Errorf("tensor is not int64 type (got type %d)", t.DataType)
	}

	if len(t.RawData)%8 != 0 {
		return nil, fmt.Errorf("invalid raw_data length for int64")
	}

	count := len(t.RawData) / 8
	result := make([]int64, count)

	for i := range count {
		result[i] = int64(binary.LittleEndian.Uint64(t.RawData[i*8 : (i+1)*8]))
	}

	return result, nil
}

// Shape returns the tensor shape
func (t *TensorProto) Shape() []int64 {
	return t.Dims
}

// ToInt32 converts tensor data to int32 slice
func (t *TensorProto) ToInt32() ([]int32, error) {
	if t.DataType != 6 { // ONNX INT32 = 6
		return nil, fmt.Errorf("tensor is not int32 type (got type %d)", t.DataType)
	}

	if len(t.RawData)%4 != 0 {
		return nil, fmt.Errorf("invalid raw_data length for int32")
	}

	count := len(t.RawData) / 4
	result := make([]int32, count)

	for i := range count {
		result[i] = int32(binary.LittleEndian.Uint32(t.RawData[i*4 : (i+1)*4]))
	}

	return result, nil
}

// ToInt16 converts tensor data to int16 slice
func (t *TensorProto) ToInt16() ([]int16, error) {
	if t.DataType != 5 { // ONNX INT16 = 5
		return nil, fmt.Errorf("tensor is not int16 type (got type %d)", t.DataType)
	}

	if len(t.RawData)%2 != 0 {
		return nil, fmt.Errorf("invalid raw_data length for int16")
	}

	count := len(t.RawData) / 2
	result := make([]int16, count)

	for i := range count {
		result[i] = int16(binary.LittleEndian.Uint16(t.RawData[i*2 : (i+1)*2]))
	}

	return result, nil
}

// ToInt8 converts tensor data to int8 slice
func (t *TensorProto) ToInt8() ([]int8, error) {
	if t.DataType != 3 { // ONNX INT8 = 3
		return nil, fmt.Errorf("tensor is not int8 type (got type %d)", t.DataType)
	}

	result := make([]int8, len(t.RawData))
	for i, b := range t.RawData {
		result[i] = int8(b)
	}

	return result, nil
}

// ToUint8 converts tensor data to uint8 slice
func (t *TensorProto) ToUint8() ([]uint8, error) {
	if t.DataType != 2 { // ONNX UINT8 = 2
		return nil, fmt.Errorf("tensor is not uint8 type (got type %d)", t.DataType)
	}

	return t.RawData, nil
}

// ToUint16 converts tensor data to uint16 slice
func (t *TensorProto) ToUint16() ([]uint16, error) {
	if t.DataType != 4 { // ONNX UINT16 = 4
		return nil, fmt.Errorf("tensor is not uint16 type (got type %d)", t.DataType)
	}

	if len(t.RawData)%2 != 0 {
		return nil, fmt.Errorf("invalid raw_data length for uint16")
	}

	count := len(t.RawData) / 2
	result := make([]uint16, count)

	for i := range count {
		result[i] = binary.LittleEndian.Uint16(t.RawData[i*2 : (i+1)*2])
	}

	return result, nil
}

// ToUint32 converts tensor data to uint32 slice
func (t *TensorProto) ToUint32() ([]uint32, error) {
	if t.DataType != 12 { // ONNX UINT32 = 12
		return nil, fmt.Errorf("tensor is not uint32 type (got type %d)", t.DataType)
	}

	if len(t.RawData)%4 != 0 {
		return nil, fmt.Errorf("invalid raw_data length for uint32")
	}

	count := len(t.RawData) / 4
	result := make([]uint32, count)

	for i := range count {
		result[i] = binary.LittleEndian.Uint32(t.RawData[i*4 : (i+1)*4])
	}

	return result, nil
}

// ToUint64 converts tensor data to uint64 slice
func (t *TensorProto) ToUint64() ([]uint64, error) {
	if t.DataType != 13 { // ONNX UINT64 = 13
		return nil, fmt.Errorf("tensor is not uint64 type (got type %d)", t.DataType)
	}

	if len(t.RawData)%8 != 0 {
		return nil, fmt.Errorf("invalid raw_data length for uint64")
	}

	count := len(t.RawData) / 8
	result := make([]uint64, count)

	for i := range count {
		result[i] = binary.LittleEndian.Uint64(t.RawData[i*8 : (i+1)*8])
	}

	return result, nil
}

// LoadTestData is a helper function to load and parse test input/output data
func LoadTestData(path string) (any, []int64, error) {
	tensor, err := LoadTensorProto(path)
	if err != nil {
		return nil, nil, err
	}

	var data any
	switch tensor.DataType {
	case 1: // FLOAT
		data, err = tensor.ToFloat32()
	case 2: // UINT8
		data, err = tensor.ToUint8()
	case 3: // INT8
		data, err = tensor.ToInt8()
	case 4: // UINT16
		data, err = tensor.ToUint16()
	case 5: // INT16
		data, err = tensor.ToInt16()
	case 6: // INT32
		data, err = tensor.ToInt32()
	case 7: // INT64
		data, err = tensor.ToInt64()
	case 12: // UINT32
		data, err = tensor.ToUint32()
	case 13: // UINT64
		data, err = tensor.ToUint64()
	default:
		return nil, nil, fmt.Errorf("unsupported data type: %d", tensor.DataType)
	}

	if err != nil {
		return nil, nil, err
	}

	return data, tensor.Shape(), nil
}
