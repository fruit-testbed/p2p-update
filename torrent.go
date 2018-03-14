package main

import (
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Metainfo holds data of torrent file
type Metainfo struct {
	metainfo.MetaInfo
	UUID       string                `bencode:"uuid:omitempty"`
	Version    string                `bencode:"version:omitempty"`
	Signatures map[string]*Signature `bencode:"signatures:omitempty"`
}

type Signature struct {
	Certificate []byte `bencode:"certificate:omitempty"`
	Info        string `bencode:"info:omitempty"`
	Signature   []byte `bencode:"info:omitempty"`
}

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
func (mi *Metainfo) Sign(keyFile string) error {
	// TODO
	return nil
}

// Verify verifies the metainfo's signature using given public key file
// See: https://stackoverflow.com/questions/10782826/digital-signature-for-a-file-using-openssl
func (mi *Metainfo) Verify(keyFile string) error {
	// TODO
	return nil
}
