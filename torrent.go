package main

import (
	"fmt"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/spacemonkeygo/openssl"
)

const (
	signatureName = "org.fruit-testbed"
)

// Metainfo holds data of torrent file
type Metainfo struct {
	metainfo.MetaInfo
	UUID       string                `bencode:"uuid:omitempty"`
	Version    string                `bencode:"version:omitempty"`
	Signatures map[string]*Signature `bencode:"signatures:omitempty"`
}

// Signature holds data signature
// See: http://www.bittorrent.org/beps/bep_0035.html
type Signature struct {
	Certificate []byte `bencode:"certificate:omitempty"`
	Info        string `bencode:"info:omitempty"`
	Signature   []byte `bencode:"info:omitempty"`
}

// NewMetainfo creates a new Metainfo instance (torrent file) of given 'filePath'.
func NewMetainfo(filePath, uuid, ver string, trackers [][]string, pieceLength int64) (*Metainfo, error) {
	mi := Metainfo{
		UUID:    uuid,
		Version: ver,
	}
	mi.AnnounceList = trackers
	mi.SetDefaults()
	info := metainfo.Info{
		PieceLength: pieceLength,
	}
	err := info.BuildFromFilePath(filePath)
	if err != nil {
		return nil, err
	}
	mi.InfoBytes, err = bencode.Marshal(info)
	if err != nil {
		return nil, err
	}
	return &mi, nil
}

// Sign signs the metainfo using given private key file.
// See: https://stackoverflow.com/questions/10782826/digital-signature-for-a-file-using-openssl
func (mi *Metainfo) Sign(key openssl.PrivateKey) error {
	var (
		bcode, sig []byte
		err        error
	)

	mi.Signatures = nil
	if bcode, err = bencode.Marshal(*mi); err != nil {
		return err
	}
	sig, err = key.SignPKCS1v15(openssl.SHA256_Method, bcode)
	if err != nil {
		return err
	}
	mi.Signatures = make(map[string]*Signature)
	mi.Signatures[signatureName] = &Signature{
		Signature: sig,
	}
	return nil
}

// Verify verifies the metainfo's signature using given public key file
// See: https://stackoverflow.com/questions/10782826/digital-signature-for-a-file-using-openssl
func (mi *Metainfo) Verify(key openssl.PublicKey) error {
	var (
		bcode []byte
		err   error
	)

	if s, ok := mi.Signatures[signatureName]; ok {
		sig := s.Signature
		sigs := mi.Signatures
		mi.Signatures = nil
		if bcode, err = bencode.Marshal(*mi); err == nil {
			err = key.VerifyPKCS1v15(openssl.SHA256_Method, bcode, sig)
		}
		mi.Signatures = sigs
		return err
	}
	return fmt.Errorf("signature is not available")
}
