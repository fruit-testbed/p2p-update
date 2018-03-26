package main

import (
	"fmt"
	"io"
	"os"
	"time"

	torrentbencode "github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/spacemonkeygo/openssl"
	"github.com/zeebo/bencode"
)

const (
	signatureName = "org.fruit-testbed"
	softwareName  = "fruit/p2pupdate"
)

// Metainfo holds data of torrent file
type Metainfo struct {
	// Fields from standard BitTorrent protocol
	Info         metainfo.Info   `bencode:"info,omitempty"`
	Announce     string          `bencode:"announce,omitempty"`
	Nodes        []metainfo.Node `bencode:"nodes,omitempty"`
	CreationDate int64           `bencode:"creation date,omitempty,ignore_unmarshal_type_error"`
	CreatedBy    string          `bencode:"created by,omitempty"`
	Encoding     string          `bencode:"encoding,omitempty"`
	Source       string          `bencode:"source,omitempty"`

	// Field from BitTorrent signing proposal
	// Reference: http://www.bittorrent.org/beps/bep_0035.html
	Signatures map[string]Signature `bencode:"signatures,omitempty"`

	// Fields proposed by Herry et.al. (see DOMINO workshop paper)
	UUID    string `bencode:"uuid,omitempty"`
	Version int    `bencode:"version,omitempty"`
}

// Signature holds data signature
// Reference: http://www.bittorrent.org/beps/bep_0035.html
type Signature struct {
	Certificate []byte `bencode:"certificate,omitempty"`
	Info        string `bencode:"info,omitempty"`
	Signature   []byte `bencode:"signature,omitempty"`
}

// NewMetainfo creates a new Metainfo instance (torrent file) of given 'filePath'.
func NewMetainfo(filename, uuid string, ver int, tracker string,
	pieceLength int64, privkey *openssl.PrivateKey) (*Metainfo, error) {
	mi := Metainfo{
		Source:       filename,
		UUID:         uuid,
		Version:      ver,
		Announce:     tracker,
		CreatedBy:    softwareName,
		Encoding:     "UTF-8",
		CreationDate: time.Now().Unix(),
		Info: metainfo.Info{
			PieceLength: pieceLength,
		},
	}
	if err := mi.Info.BuildFromFilePath(filename); err != nil {
		return nil, err
	}
	mi.Info.Name = fmt.Sprintf("%s-v%d-%s", mi.UUID, mi.Version, mi.Info.Name)
	if err := mi.Sign(*privkey); err != nil {
		return nil, err
	}
	return &mi, nil
}

// ReadMetainfo reads the Metainfo from given Reader.
func ReadMetainfo(r io.Reader) (*Metainfo, error) {
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
	return ReadMetainfo(f)
}

// Write writes the Metainfo to given Writer
func (mi *Metainfo) Write(w io.Writer) error {
	var (
		b   []byte
		err error
	)

	if b, err = bencode.EncodeBytes(*mi); err != nil {
		return fmt.Errorf("failed to generating bencode from Metainfo: %v", err)
	}
	_, err = w.Write(b)
	return err
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

// torrentMetainfo returns the anacrolix's torrent Metainfo.
func (mi *Metainfo) torrentMetainfo() (*metainfo.MetaInfo, error) {
	mm := metainfo.MetaInfo{
		Announce:     mi.Announce,
		Nodes:        mi.Nodes,
		CreationDate: mi.CreationDate,
		CreatedBy:    mi.CreatedBy,
		Encoding:     mi.Encoding,
	}
	var err error
	if mm.InfoBytes, err = torrentbencode.Marshal(mi.Info); err != nil {
		return nil, fmt.Errorf("failed encoding InfoBytes: %v", err)
	}
	return &mm, nil
}
