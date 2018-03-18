package main

import (
	"fmt"
	"io"
	"os"
	"time"

	tb "github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/spacemonkeygo/openssl"
	"github.com/zeebo/bencode"
)

const (
	signatureName = "org.fruit-testbed"
	softwareName  = "fruit/p2pupdate"

	// DefaultPieceLength holds the default length of file-piece
	DefaultPieceLength = 32 * 1024
)

var (
	// PublicAnnounceList contains a list of public BitTorrent trackers
	PublicAnnounceList = [][]string{
		{"udp://tracker.openbittorrent.com:80"},
		{"udp://tracker.publicbt.com:80"},
		{"udp://tracker.istole.it:6969"},
	}
)

// Metainfo holds data of torrent file
type Metainfo struct {
	// Fields from standard BitTorrent protocol
	InfoBytes    metainfo.Info         `bencode:"info,omitempty"`
	AnnounceList metainfo.AnnounceList `bencode:"announce-list,omitempty"`
	Nodes        []metainfo.Node       `bencode:"nodes,omitempty"`
	CreationDate int64                 `bencode:"creation date,omitempty,ignore_unmarshal_type_error"`
	CreatedBy    string                `bencode:"created by,omitempty"`
	Encoding     string                `bencode:"encoding,omitempty"`

	// Field from BitTorrent signing proposal
	// Reference: http://www.bittorrent.org/beps/bep_0035.html
	Signatures map[string]Signature `bencode:"signatures,omitempty"`

	// Fields proposed by Herry et.al.
	UUID    string `bencode:"uuid,omitempty"`
	Version string `bencode:"version,omitempty"`
}

// Signature holds data signature
// Reference: http://www.bittorrent.org/beps/bep_0035.html
type Signature struct {
	Certificate []byte `bencode:"certificate,omitempty"`
	Info        string `bencode:"info,omitempty"`
	Signature   []byte `bencode:"signature,omitempty"`
}

// NewMetainfo creates a new Metainfo instance (torrent file) of given 'filePath'.
func NewMetainfo(filename, uuid, ver string, trackers [][]string,
	pieceLength int64, privkey *openssl.PrivateKey) (*Metainfo, error) {
	mi := Metainfo{
		UUID:         uuid,
		Version:      ver,
		AnnounceList: trackers,
		CreatedBy:    softwareName,
		Encoding:     "UTF-8",
		CreationDate: time.Now().Unix(),
	}
	info := metainfo.Info{
		PieceLength: pieceLength,
	}
	if err := info.BuildFromFilePath(filename); err != nil {
		return nil, err
	}
	mi.InfoBytes = info
	if err := mi.Sign(*privkey); err != nil {
		return nil, err
	}
	return &mi, nil
}

// LoadMetainfo reads the Metainfo from given Reader.
func LoadMetainfo(r io.Reader) (*Metainfo, error) {
	var mi Metainfo
	return &mi, bencode.NewDecoder(r).Decode(&mi)
}

// LoadMetainfoFromFile reads a torrentfile++ and returns its corresponding
// Metainfo instance
func LoadMetainfoFromFile(filename string) (*Metainfo, error) {
	var (
		f   *os.File
		err error
	)

	if f, err = os.Open(filename); err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadMetainfo(f)
}

// Write writes the Metainfo to given Writer
func (mi *Metainfo) Write(w io.Writer) error {
	return bencode.NewEncoder(w).Encode(*mi)
}

// WriteToFile writes the Metainfo to given filename
func (mi *Metainfo) WriteToFile(filename string) error {
	var (
		f   *os.File
		err error
	)

	if f, err = os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644); err != nil {
		return err
	}
	defer f.Close()
	return mi.Write(f)
}

// Sign signs the Metainfo using given private key file.
// Reference: https://stackoverflow.com/questions/10782826/digital-signature-for-a-file-using-openssl
func (mi *Metainfo) Sign(key openssl.PrivateKey) error {
	var (
		data, sig []byte
		err       error
	)

	mi.Signatures = nil
	if data, err = bencode.EncodeBytes(*mi); err != nil {
		return err
	}
	sig, err = key.SignPKCS1v15(openssl.SHA256_Method, data)
	if err != nil {
		return err
	}
	mi.Signatures = make(map[string]Signature)
	mi.Signatures[signatureName] = Signature{
		Signature: sig,
	}
	return nil
}

// Verify verifies the Metainfo's signature using given public key file
// Reference: https://stackoverflow.com/questions/10782826/digital-signature-for-a-file-using-openssl
func (mi *Metainfo) Verify(key openssl.PublicKey) error {
	var (
		data []byte
		err  error
	)

	if s, ok := mi.Signatures[signatureName]; ok {
		sigs := mi.Signatures
		mi.Signatures = nil
		if data, err = bencode.EncodeBytes(*mi); err == nil {
			err = key.VerifyPKCS1v15(openssl.SHA256_Method, data, s.Signature)
		}
		mi.Signatures = sigs
		return err
	}
	return fmt.Errorf("signature is not available")
}

func (m *Metainfo) torrentMetainfo() (*metainfo.MetaInfo, error) {
	mi := metainfo.MetaInfo{
		AnnounceList: m.AnnounceList,
		Nodes:        m.Nodes,
		CreationDate: m.CreationDate,
		CreatedBy:    m.CreatedBy,
		Encoding:     m.Encoding,
	}
	var err error
	if mi.InfoBytes, err = tb.Marshal(m.InfoBytes); err != nil {
		return nil, fmt.Errorf("failed encoding InfoBytes: %v", err)
	}
	return &mi, nil
}
