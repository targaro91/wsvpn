package servers

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"

	"github.com/Doridian/wsvpn/shared"
	"github.com/Doridian/wsvpn/shared/commands"
	"github.com/Doridian/wsvpn/shared/sockets"
	"github.com/Doridian/wsvpn/shared/sockets/adapters"
	"github.com/google/uuid"
	"github.com/songgao/water"
)

func (s *Server) serveSocket(w http.ResponseWriter, r *http.Request) {
	var err error

	clientIdUUID, err := uuid.NewRandom()
	if err != nil {
		log.Printf("[%s] Error creating client ID: %v", s.ServerID, err)
		http.Error(w, "Error creating client ID", http.StatusInternalServerError)
		return
	}

	clientId := clientIdUUID.String()

	if r.TLS != nil {
		log.Printf("[%s] TLS %s connection established with cipher=%s", clientId, shared.TlsVersionString(r.TLS.Version), tls.CipherSuiteName(r.TLS.CipherSuite))
	} else {
		log.Printf("[%s] Unencrypted connection established", clientId)
	}

	authOk := s.handleSocketAuth(clientId, w, r)
	if !authOk {
		return
	}

	var slot uint64 = 1
	maxSlot := s.VPNNet.GetClientSlots() + 1
	s.slotMutex.Lock()
	for s.usedSlots[slot] {
		slot = slot + 1
		if slot > maxSlot {
			s.slotMutex.Unlock()
			log.Printf("[%s] Cannot connect new client: IP slots exhausted", clientId)
			http.Error(w, "IP slots exhausted", http.StatusInternalServerError)
			return
		}
	}
	s.usedSlots[slot] = true
	s.slotMutex.Unlock()

	defer func() {
		s.slotMutex.Lock()
		delete(s.usedSlots, slot)
		s.slotMutex.Unlock()
	}()

	var adapter adapters.SocketAdapter
	if r.Proto == "webtransport" && s.HTTP3Enabled {
		adapter, err = s.serveWebTransport(w, r)
	} else {
		adapter, err = s.serveWebSocket(w, r)
	}

	if err != nil {
		log.Printf("[%s] Error upgrading connection: %v", clientId, err)
		return
	}

	defer adapter.Close()

	log.Printf("[%s] Upgraded connection to %s", clientId, adapter.Name())

	ipClient, err := s.VPNNet.GetIPAt(int(slot) + 1)
	if err != nil {
		log.Printf("[%s] Error transforming client IP: %v", clientId, err)
		return
	}

	var iface *water.Interface
	if s.Mode == shared.VPN_MODE_TAP {
		iface = s.tapIface
	} else {
		s.ifaceCreationMutex.Lock()
		tunConfig := water.Config{
			DeviceType: water.TUN,
		}
		err = s.extendTUNConfig(&tunConfig)
		if err != nil {
			s.ifaceCreationMutex.Unlock()
			log.Printf("[%s] Error extending TUN config: %v", clientId, err)
			return
		}

		iface, err = water.New(tunConfig)
		s.ifaceCreationMutex.Unlock()
		if err != nil {
			log.Printf("[%s] Error creating new TUN: %v", clientId, err)
			return
		}

		defer iface.Close()

		log.Printf("[%s] Assigned interface %s", clientId, iface.Name())

		err = s.configIface(iface, ipClient)
		if err != nil {
			log.Printf("[%s] Error configuring interface: %v", clientId, err)
			return
		}
	}

	socket := sockets.MakeSocket(clientId, adapter, iface, s.Mode == shared.VPN_MODE_TUN)
	if s.SocketGroup != nil {
		socket.SetPacketHandler(s.SocketGroup)
	}
	socket.SetMTU(s.mtu)
	defer socket.Close()

	log.Printf("[%s] Connection fully established", clientId)
	defer log.Printf("[%s] Disconnected", clientId)

	socket.Serve()
	socket.MakeAndSendCommand(&commands.InitParameters{
		ClientID:   clientId,
		ServerID:   s.ServerID,
		Mode:       s.Mode.ToString(),
		DoIpConfig: s.DoRemoteIpConfig,
		IpAddress:  fmt.Sprintf("%s/%d", ipClient.String(), s.VPNNet.GetSize()),
		MTU:        s.mtu,
	})
	socket.Wait()
}
