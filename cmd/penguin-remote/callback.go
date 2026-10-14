package main

import "github.com/Prism-Shadow/penguin-remote-client/internal/remote"

func identityCallbackPage(language string, subscription remote.Subscription) callbackPage {
	heading := "登录成功"
	detail := "设备身份已更新。登录不会领取槽位，请返回 Penguin Remote 点击开启。"
	tone := "success"
	if language == "en" {
		heading = "Signed in"
		detail = "Device identity updated. Sign-in does not take a slot. Return to Penguin Remote and turn access on."
	}
	if subscription.Status == remote.EntitlementStateExpired {
		tone = "warning"
		detail = "设备身份已保存。订阅已到期，续订后刷新订阅信息，无需重新登录。"
		if language == "en" {
			detail = "Device identity saved. The subscription expired. Renew and refresh subscription information; no new sign-in is needed."
		}
	}
	if subscription.Status == remote.EntitlementStateNone {
		tone = "warning"
		detail = "设备身份已保存。请开通 Remote 后刷新订阅信息。"
		if language == "en" {
			detail = "Device identity saved. Subscribe to Remote and refresh subscription information."
		}
	}
	return callbackPage{Heading: heading, Detail: detail, Tone: tone}
}
