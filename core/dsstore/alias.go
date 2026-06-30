package dsstore

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

const macEpochOffset = 2082844800 // seconds between 1904-01-01 and 1970-01-01

// Alias Manager v2 tags
const (
	tagCarbonFolderName = 0
	tagCNIDPath         = 1
	tagCarbonPath       = 2
	tagUnicodeFilename  = 14
	tagUnicodeVolName   = 15
	tagHiResVolDate     = 16
	tagHiResCreateDate  = 17
	tagPosixPath        = 18
	tagPosixMountpoint  = 19
)

func be16(b *bytes.Buffer, v uint16) { binary.Write(b, binary.BigEndian, v) }
func be32(b *bytes.Buffer, v uint32) { binary.Write(b, binary.BigEndian, v) }
func be64(b *bytes.Buffer, v uint64) { binary.Write(b, binary.BigEndian, v) }

// pascal writes a length-prefixed string padded to total bytes.
func pascal(b *bytes.Buffer, s string, total int) {
	bs := []byte(s)
	b.WriteByte(byte(len(bs)))
	b.Write(bs)
	for i := 1 + len(bs); i < total; i++ {
		b.WriteByte(0)
	}
}

// tag writes a tagged extra (tag, len, data) padded to even length.
func tag(b *bytes.Buffer, t uint16, data []byte) {
	be16(b, t)
	be16(b, uint16(len(data)))
	b.Write(data)
	if len(data)&1 == 1 {
		b.WriteByte(0)
	}
}

func utf16be(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, len(u)*2)
	for i, c := range u {
		binary.BigEndian.PutUint16(out[i*2:], c)
	}
	return out
}

// buildBackgroundAlias builds an Alias Manager v2 record for a file inside a
// volume (the dmg). It points at /Volumes/<vol>/<folder>/<file> and resolves by
// volume name + relative path, so it works on any machine that mounts the dmg
// under <vol> (= the build's volname). Ports mac_alias.Alias.for_file/to_bytes.
func buildBackgroundAlias(volName, folderName, fileName string, volCrtimeUnix, fileCrtimeUnix int64, folderCNID, fileCNID uint32) []byte {
	volDate := uint32(volCrtimeUnix + macEpochOffset)
	crDate := uint32(fileCrtimeUnix + macEpochOffset)

	body := new(bytes.Buffer)
	// v2 fixed block
	be16(body, 0)                  // kind = file
	pascal(body, volName, 28)      // volume name (carbon)
	be32(body, volDate)            // volume creation date
	body.WriteString("H+")         // fs type
	be16(body, 5)                  // disk type = ejectable (dmg)
	be32(body, folderCNID)         // parent (folder) cnid
	pascal(body, fileName, 64)     // file name (carbon)
	be32(body, fileCNID)           // file cnid
	be32(body, crDate)             // file creation date
	body.Write([]byte{0, 0, 0, 0}) // creator code
	body.Write([]byte{0, 0, 0, 0}) // type code
	be16(body, 0xffff)             // levels_from = -1
	be16(body, 0xffff)             // levels_to = -1
	be32(body, 0)                  // volume attribute flags
	be16(body, 0)                  // fs id
	body.Write(make([]byte, 10))   // reserved

	tag(body, tagCarbonFolderName, []byte(folderName))
	hi := new(bytes.Buffer)
	be64(hi, uint64(volDate)*65536)
	tag(body, tagHiResVolDate, hi.Bytes())
	hc := new(bytes.Buffer)
	be64(hc, uint64(crDate)*65536)
	tag(body, tagHiResCreateDate, hc.Bytes())

	cnp := new(bytes.Buffer)
	be32(cnp, folderCNID)
	tag(body, tagCNIDPath, cnp.Bytes())

	carbon := volName + ":" + folderName + ":\x00" + fileName
	tag(body, tagCarbonPath, []byte(carbon))

	uf := utf16be(fileName)
	uft := new(bytes.Buffer)
	be16(uft, uint16(len(uf)/2))
	uft.Write(uf)
	tag(body, tagUnicodeFilename, uft.Bytes())

	uv := utf16be(volName)
	uvt := new(bytes.Buffer)
	be16(uvt, uint16(len(uv)/2))
	uvt.Write(uv)
	tag(body, tagUnicodeVolName, uvt.Bytes())

	tag(body, tagPosixPath, []byte("/"+folderName+"/"+fileName))
	tag(body, tagPosixMountpoint, []byte("/Volumes/"+volName))

	be16(body, 0xffff) // terminator tag -1
	be16(body, 0)      // terminator len

	out := new(bytes.Buffer)
	out.Write([]byte{0, 0, 0, 0}) // appinfo
	be16(out, 0)                  // recsize placeholder
	be16(out, 2)                  // version
	out.Write(body.Bytes())
	b := out.Bytes()
	binary.BigEndian.PutUint16(b[4:6], uint16(len(b))) // patch record size
	return b
}
