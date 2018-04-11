package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"time"

	torrentbencode "github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/zeebo/bencode"
)

// Notification holds the data of update notification
type Notification struct {
	// Fields from standard BitTorrent protocol
	Info         metainfo.Info   `bencode:"info,omitempty"`
	Announce     string          `bencode:"announce,omitempty"`
	Nodes        []metainfo.Node `bencode:"nodes,omitempty"`
	CreationDate int64           `bencode:"creation date,omitempty,ignore_unmarshal_type_error"`
	CreatedBy    string          `bencode:"created by,omitempty"`
	Encoding     string          `bencode:"encoding,omitempty"`

	// Field from BitTorrent signing proposal
	// Reference: http://www.bittorrent.org/beps/bep_0035.html
	Signatures map[string]Signature `bencode:"signatures,omitempty"`

	// Fields proposed by Herry et.al. (see DOMINO workshop paper)
	UUID    string `bencode:"uuid,omitempty"`
	Version uint64 `bencode:"version,omitempty"`
}

// Signature holds data signature
// Reference: http://www.bittorrent.org/beps/bep_0035.html
type Signature struct {
	Certificate []byte `bencode:"certificate,omitempty"`
	Info        string `bencode:"info,omitempty"`
	Signature   []byte `bencode:"signature,omitempty"`
}

// NewNotification creates a new Notification instance of given update's filename.
func NewNotification(filename, uuid string, ver uint64, tracker string,
	pieceLength int64, privkey *rsa.PrivateKey) (*Notification, error) {
	mi := Notification{
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
	if err := mi.Sign(privkey); err != nil {
		return nil, err
	}
	return &mi, nil
}

// ReadNotification reads the Notification from given Reader.
func ReadNotification(r io.Reader) (*Notification, error) {
	var mi Notification
	return &mi, bencode.NewDecoder(r).Decode(&mi)
}

// LoadNotificationFromFile reads an update notification from given filename
// and returns its corresponding Notification instance
func LoadNotificationFromFile(filename string) (*Notification, error) {
	var (
		f   *os.File
		err error
	)

	if f, err = os.Open(filename); err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadNotification(f)
}

// Write writes the Notification to given Writer.
func (mi *Notification) Write(w io.Writer) error {
	var (
		b   []byte
		err error
	)

	if b, err = bencode.EncodeBytes(*mi); err != nil {
		return fmt.Errorf("failed to generating bencode from Notification: %v", err)
	}
	_, err = w.Write(b)
	return err
}

// Sign signs the Notification using given private key file.
// Reference: https://stackoverflow.com/questions/10782826/digital-signature-for-a-file-using-openssl
func (mi *Notification) Sign(key *rsa.PrivateKey) error {
	var (
		data, sig []byte
		err       error
	)

	mi.Signatures = nil
	if data, err = bencode.EncodeBytes(*mi); err != nil {
		return err
	}
	hashed := sha256.Sum256(data)
	sig, err = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		return err
	}
	mi.Signatures = make(map[string]Signature)
	mi.Signatures[signatureName] = Signature{
		Signature: sig,
	}
	return nil
}

// Verify verifies the Notification's signature using given public key file
// Reference: https://stackoverflow.com/questions/10782826/digital-signature-for-a-file-using-openssl
func (mi *Notification) Verify(pub *rsa.PublicKey) error {
	var (
		data []byte
		err  error
	)

	if s, ok := mi.Signatures[signatureName]; ok {
		sigs := mi.Signatures
		mi.Signatures = nil
		if data, err = bencode.EncodeBytes(*mi); err == nil {
			hashed := sha256.Sum256(data)
			err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, hashed[:], s.Signature)
		}
		mi.Signatures = sigs
		return err
	}
	return fmt.Errorf("signature is not available")
}

// torrentMetainfo returns the anacrolix's torrent Metainfo.
func (mi *Notification) torrentMetainfo() (*metainfo.MetaInfo, error) {
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
