package main

import (
"encoding/binary"
"fmt"
"io/ioutil"
"math"
"os"
)

const (
numVoices    = 53
styleDim     = 510
embeddingDim = 256
totalFloats  = styleDim * embeddingDim // 130560
floatSize    = 4
voiceSize    = totalFloats * floatSize // 522240
)

type VoiceData []float32

var cachedVoices []VoiceData

func LoadAllVoices(path string) ([]VoiceData, error) {
data, err := ioutil.ReadFile(path)
if err != nil { return nil, err }
if len(data) < numVoices*voiceSize {
return nil, fmt.Errorf("voices.bin too small: expected %d, got %d", numVoices*voiceSize, len(data))
}
voices := make([]VoiceData, numVoices)
for i := 0; i < numVoices; i++ {
offset := i * voiceSize
v := make(VoiceData, totalFloats)
for j := 0; j < totalFloats; j++ {
bits := binary.LittleEndian.Uint32(data[offset+j*floatSize : offset+(j+1)*floatSize])
v[j] = math.Float32frombits(bits)
}
voices[i] = v
}
cachedVoices = voices
return voices, nil
}

func ParseRawVoice(data []byte) VoiceData {
	vd := make(VoiceData, totalFloats)
	for i := 0; i < totalFloats && (i+1)*4 <= len(data); i++ {
		bits := binary.LittleEndian.Uint32(data[i*4 : (i+1)*4])
		val := math.Float32frombits(bits)
		// Safety Clamp: prevent numerical instability from blowing up ONNX Runtime arenas
		if val > 2.0 { val = 2.0 }
		if val < -2.0 { val = -2.0 }
		vd[i] = val
	}
	return vd
}

func l2Norm(v []float32) float32 {
var sum float32
for _, x := range v { sum += x * x }
return float32(math.Sqrt(float64(sum)))
}

func MixLinear(indices []int, weights []float32) VoiceData {
out := make(VoiceData, totalFloats)
var total float32
for _, w := range weights { total += w }
if total == 0 { return out }
for i := range out {
for vIdx, weight := range weights {
out[i] += cachedVoices[indices[vIdx]][i] * (weight / total)
}
}
return out
}

func MixSLERP(indices []int, weights []float32) VoiceData {
out := MixLinear(indices, weights)
var avgMag float32
for _, idx := range indices { avgMag += l2Norm(cachedVoices[idx]) }
avgMag /= float32(len(indices))
currentMag := l2Norm(out)
if currentMag > 0 {
scale := avgMag / currentMag
for i := range out { out[i] *= scale }
}
return out
}

func MixEQ(indices []int, binWeights [][]float32) VoiceData {
out := make(VoiceData, totalFloats)
for b := 0; b < styleDim; b++ {
var totalWeight float32
for vIdx := range indices { totalWeight += binWeights[vIdx][b] }
if totalWeight == 0 { continue }

for e := 0; e < embeddingDim; e++ {
idx := b*embeddingDim + e
for vIdx := range indices {
out[idx] += cachedVoices[indices[vIdx]][idx] * (binWeights[vIdx][b] / totalWeight)
}
}
}
return out
}

func CreateSandbox(masterPath, sandboxPath string, sid int, newVoice VoiceData) error {
data, err := ioutil.ReadFile(masterPath)
if err != nil { return err }
offset := sid * voiceSize
for i, val := range newVoice {
bits := math.Float32bits(val)
binary.LittleEndian.PutUint32(data[offset+i*floatSize : offset+(i+1)*floatSize], bits)
}
return ioutil.WriteFile(sandboxPath, data, 0644)
}

func UpdateSandboxRange(sandboxPath string, sid int, startBin, endBin int, newVoice VoiceData) error {
	f, err := os.OpenFile(sandboxPath, os.O_RDWR, 0644)
	if err != nil { return err }
	defer f.Close()

	if startBin < 0 { startBin = 0 }
	if endBin > styleDim { endBin = styleDim }

	voiceOffset := int64(sid * voiceSize)
	for b := startBin; b < endBin; b++ {
		binOffset := voiceOffset + int64(b*embeddingDim*floatSize)
		if _, err := f.Seek(binOffset, 0); err != nil { return err }
		for e := 0; e < embeddingDim; e++ {
			val := newVoice[b*embeddingDim+e]
			if err := binary.Write(f, binary.LittleEndian, val); err != nil { return err }
		}
	}
	return nil
}

func ApplySave(masterPath string, sid int, newVoice VoiceData) error {
f, err := os.OpenFile(masterPath, os.O_RDWR, 0644)
if err != nil { return err }
defer f.Close()
offset := int64(sid * voiceSize)
if _, err := f.Seek(offset, 0); err != nil { return err }
for _, val := range newVoice {
if err := binary.Write(f, binary.LittleEndian, val); err != nil { return err }
}
cachedVoices[sid] = newVoice
return nil
}
