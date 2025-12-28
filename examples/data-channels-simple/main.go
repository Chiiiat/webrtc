// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

// simple-datachannel is a simple datachannel demo that auto connects.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pion/webrtc/v4"
)

func main() {
	var pc *webrtc.PeerConnection

	setupOfferHandler(&pc)     // 处理 offer 请求
	setupCandidateHandler(&pc) // 处理 ICE 候选
	setupStaticHandler()       // 提供静态文件服务

	// 启动 HTTP 服务器
	fmt.Println("🚀 Signaling server started on http://localhost:8080")
	//nolint:gosec
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Failed to start server: %v\n", err)
	}
}

// 数据通道事件处理
func setupOfferHandler(pc **webrtc.PeerConnection) {
	http.HandleFunc("/offer", func(responseWriter http.ResponseWriter, r *http.Request) {
		// 接收客户端发送的 SDP offer
		var offer webrtc.SessionDescription
		if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
			http.Error(responseWriter, err.Error(), http.StatusBadRequest)

			return
		}

		// 创建新的 PeerConnection 实例
		var err error
		*pc, err = webrtc.NewPeerConnection(webrtc.Configuration{
			// 配置 STUN 服务器以支持 NAT 穿透
			ICEServers: []webrtc.ICEServer{
				{URLs: []string{"stun:stun.l.google.com:19302"}},
			},
			// 设置 BundlePolicy 和 RTCPMuxPolicy 优化连接，以得到更好的浏览器兼容性
			BundlePolicy:  webrtc.BundlePolicyBalanced,
			RTCPMuxPolicy: webrtc.RTCPMuxPolicyRequire,
		})
		if err != nil {
			http.Error(responseWriter, err.Error(), http.StatusInternalServerError)

			return
		}

		setupICECandidateHandler(*pc)
		setupDataChannelHandler(*pc)

		if err := processOffer(*pc, offer, responseWriter); err != nil {
			http.Error(responseWriter, err.Error(), http.StatusInternalServerError)

			return
		}
	})
}

func setupICECandidateHandler(pc *webrtc.PeerConnection) {
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			fmt.Printf("🌐 New ICE candidate: %s\n", c.Address)
		}
	})
}

// 数据通道事件处理
func setupDataChannelHandler(pc *webrtc.PeerConnection) {
	pc.OnDataChannel(func(d *webrtc.DataChannel) {
		// 连接打开后自动发送欢迎消息
		d.OnOpen(func() {
			fmt.Println("✅ DataChannel opened (Server)")
			if sendErr := d.SendText("Hello from Go server 👋"); sendErr != nil {
				fmt.Printf("Failed to send text: %v\n", sendErr)
			}
		})
		// 接收来自客户端的消息
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			fmt.Printf("📩 Received: %s\n", string(msg.Data))
		})
	})
}

func processOffer(
	pc *webrtc.PeerConnection,
	offer webrtc.SessionDescription,
	responseWriter http.ResponseWriter,
) error {
	// 接收并设置远端描述(offer)
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}

	// 创建并返回 answer
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}

	// Set local description
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}

	// 等待 ICE 候选收集完成
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	finalAnswer := pc.LocalDescription() // 获取最终的本地描述
	if finalAnswer == nil {
		//nolint:err113
		return fmt.Errorf("local description is nil after ICE gathering")
	}

	// 发送完整的 answer 回客户端
	responseWriter.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(responseWriter).Encode(*finalAnswer); err != nil {
		fmt.Printf("Failed to encode answer: %v\n", err)
	}

	return nil
}

// 处理并添加来自客户端的 ICE 候选信息
// 用于建立点对点连接
func setupCandidateHandler(pc **webrtc.PeerConnection) {
	http.HandleFunc("/candidate", func(responseWriter http.ResponseWriter, r *http.Request) {
		var candidate webrtc.ICECandidateInit
		if err := json.NewDecoder(r.Body).Decode(&candidate); err != nil {
			http.Error(responseWriter, err.Error(), http.StatusBadRequest)

			return
		}
		if *pc != nil {
			if err := (*pc).AddICECandidate(candidate); err != nil {
				fmt.Println("Failed to add candidate", err)
			}
		}
	})
}

func setupStaticHandler() {
	// demo.html
	http.HandleFunc("/", func(responseWriter http.ResponseWriter, r *http.Request) {
		http.ServeFile(responseWriter, r, "./demo.html")
	})
}
