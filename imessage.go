// mautrix-imessage - A Matrix-iMessage puppeting bridge.
// Copyright (C) 2021 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"time"

	log "maunium.net/go/maulogger/v2"

	"go.mau.fi/mautrix-imessage/imessage"
)

type iMessageHandler struct {
	bridge *IMBridge
	log    log.Logger
	stop   chan struct{}
}

func NewiMessageHandler(bridge *IMBridge) *iMessageHandler {
	return &iMessageHandler{
		bridge: bridge,
		log:    bridge.Log.Sub("iMessage"),
		stop:   make(chan struct{}),
	}
}

func (imh *iMessageHandler) Start() {
	messages := imh.bridge.IM.MessageChan()
	readReceipts := imh.bridge.IM.ReadReceiptChan()
	typingNotifications := imh.bridge.IM.TypingNotificationChan()
	chats := imh.bridge.IM.ChatChan()
	contacts := imh.bridge.IM.ContactChan()
	messageStatuses := imh.bridge.IM.MessageStatusChan()
	for {
		start := time.Now()
		var thing string
		select {
		case msg := <-messages:
			imh.HandleMessage(msg)
			thing = "message"
		case rr := <-readReceipts:
			imh.HandleReadReceipt(rr)
			thing = "read receipt"
		case notif := <-typingNotifications:
			imh.HandleTypingNotification(notif)
			thing = "typing notification"
		case chat := <-chats:
			imh.HandleChat(chat)
			thing = "chat"
		case contact := <-contacts:
			imh.HandleContact(contact)
			thing = "contact"
		case status := <-messageStatuses:
			imh.HandleMessageStatus(status)
			thing = "message status"
		case <-imh.stop:
			return
		}
		imh.log.Debugfln(
			"Handled %s in %s (queued: %dm/%dr/%dt/%dch/%dct/%ds)",
			thing, time.Since(start),
			len(messages), len(readReceipts), len(typingNotifications), len(chats), len(contacts), len(messageStatuses),
		)
	}
}

const PortalBufferTimeout = 10 * time.Second

func (imh *iMessageHandler) HandleMessage(msg *imessage.Message) {
	// TODO trace log
	//imh.log.Debugfln("Received incoming message: %+v", msg)
	portal := imh.bridge.GetPortalByGUID(msg.ChatGUID)
	if len(portal.MXID) == 0 {
		if portal.ThreadID == "" {
			portal.ThreadID = msg.ThreadID
		}
		portal.log.Infoln("Creating Matrix room to handle message")
		err := portal.CreateMatrixRoom(nil, nil)
		if err != nil {
			imh.log.Warnfln("Failed to create Matrix room to handle message: %v", err)
			return
		}
	}
	select {
	case portal.Messages <- msg:
	case <-time.After(PortalBufferTimeout):
		imh.log.Errorln("Portal message buffer is still full after 10 seconds, dropping message %s", msg.GUID)
	}
}

func (imh *iMessageHandler) HandleMessageStatus(status *imessage.SendMessageStatus) {
	portal := imh.bridge.GetPortalByGUID(status.ChatGUID)
	if len(portal.MXID) == 0 {
		imh.log.Debugfln("Ignoring message status for message from unknown portal %s/%s", status.GUID, status.ChatGUID)
		return
	}
	select {
	case portal.MessageStatuses <- status:
	case <-time.After(PortalBufferTimeout):
		imh.log.Errorln("Portal message status buffer is still full after 10 seconds, dropping %+v", *status)
	}
}

func (imh *iMessageHandler) HandleReadReceipt(rr *imessage.ReadReceipt) {
	portal := imh.bridge.GetPortalByGUID(rr.ChatGUID)
	if len(portal.MXID) == 0 {
		imh.log.Debugfln("Ignoring read receipt in unknown portal %s", rr.ChatGUID)
		return
	}
	select {
	case portal.ReadReceipts <- rr:
	case <-time.After(PortalBufferTimeout):
		imh.log.Errorln("Portal read receipt buffer is still full after 10 seconds, dropping %+v", *rr)
	}
}

func (imh *iMessageHandler) HandleTypingNotification(notif *imessage.TypingNotification) {
	portal := imh.bridge.GetPortalByGUID(notif.ChatGUID)
	if len(portal.MXID) == 0 {
		return
	}
	_, err := portal.MainIntent().UserTyping(portal.MXID, notif.Typing, 60*time.Second)
	if err != nil {
		action := "typing"
		if !notif.Typing {
			action = "not typing"
		}
		portal.log.Warnln("Failed to mark %s as %s in %s: %v", portal.MainIntent().UserID, action, portal.MXID, err)
	}
}

func (imh *iMessageHandler) HandleChat(chat *imessage.ChatInfo) {
	chat.Identifier = imessage.ParseIdentifier(chat.JSONChatGUID)
	portal := imh.bridge.GetPortalByGUID(chat.Identifier.String())
	if len(portal.MXID) > 0 {
		portal.log.Infoln("Syncing Matrix room to handle chat command")
		portal.SyncWithInfo(chat)
	} else if !chat.NoCreateRoom {
		portal.log.Infoln("Creating Matrix room to handle chat command")
		err := portal.CreateMatrixRoom(chat, nil)
		if err != nil {
			imh.log.Warnfln("Failed to create Matrix room to handle chat command: %v", err)
			return
		}
	}
}

func (imh *iMessageHandler) HandleContact(contact *imessage.Contact) {
	puppet := imh.bridge.GetPuppetByGUID(contact.UserGUID)
	if len(puppet.MXID) > 0 {
		puppet.log.Infoln("Syncing Puppet to handle contact command")
		puppet.SyncWithContact(contact)
	}
}

func (imh *iMessageHandler) Stop() {
	close(imh.stop)
}
