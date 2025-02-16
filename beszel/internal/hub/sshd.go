package hub

import (
	"beszel"
	"database/sql"
	"net"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/crypto/ssh"
)

func (h *Hub) startSSHServer(addr string) error {
	privateKey, err := h.getSSHKey()
	if err != nil {
		h.Logger().Error("Failed to get SSH key: ", "err", err.Error())
		return err
	}

	config := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-" + beszel.AppName + "-" + beszel.Version,
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{
				Extensions: map[string]string{
					"fingerprint": ssh.FingerprintSHA256(key),
				},
			}, nil
		},
	}
	config.AddHostKey(privateKey)

	sshListener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	go func() {
		for {
			conn, err := sshListener.Accept()
			if err != nil {
				h.Logger().Warn("Failed to accept incoming connection", "err", err)
				continue
			}

			go h.acceptConn(conn, config)
		}
	}()

	return nil
}

func (h *Hub) acceptConn(c net.Conn, config *ssh.ServerConfig) {

	h.Logger().Info("new system connected", "address", c.RemoteAddr())
	// Before use, a handshake must be performed on the incoming net.Conn.
	sshConn, chans, reqs, err := ssh.NewServerConn(c, config)
	if err != nil {
		h.Logger().Info("Failed to SSH handshake", "err", err)
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	// TODO on valid api key associate to user and automatically allow

	fingerprint := sshConn.Permissions.Extensions["fingerprint"]
	_, err = h.FindFirstRecordByFilter(
		"2hz5ncl8tizk5nx", // systems collection
		"status!='paused' && fingerprint={:fingerprint}",
		dbx.Params{"fingerprint": fingerprint},
	)
	if err != nil {
		if err == sql.ErrNoRows {
			h.Logger().Info("Unknown client tried to connect", "fingerprint", fingerprint, "address", c.RemoteAddr())

			_, err = h.FindFirstRecordByFilter(
				"new_systems",
				"fingerprint={:fingerprint}",
				dbx.Params{"fingerprint": fingerprint},
			)

			if err != nil {
				if err == sql.ErrNoRows {
					collection, err := h.FindCollectionByNameOrId("new_systems")
					if err != nil {
						h.Logger().Error("failed to get new_systems collection", "err", err)
						return
					}

					newSystemRecord := core.NewRecord(collection)
					newSystemRecord.Set("hostname", sshConn.User())
					newSystemRecord.Set("fingerprint", fingerprint)
					newSystemRecord.Set("address", c.RemoteAddr())

					err = h.Save(newSystemRecord)
					if err != nil {
						h.Logger().Error("failed to save pending system record", "err", err)
						return
					}
				} else {
					h.Logger().Error("failed to get pending system records", "err", err)
					return
				}
			}

			return
		}
		h.Logger().Error("Failed to fetch client record", "err", err)
		return
	}

	for channel := range chans {
		if channel.ChannelType() != "stats" {
			channel.Reject(ssh.Prohibited, "Invalid channel type")
			h.Logger().Warn("client tried to open an invalid channel", "type", channel.ChannelType(), "address", c.RemoteAddr())
			return
		}

	}
}
