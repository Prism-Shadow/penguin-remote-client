# 用户提示完整清单 / User messages

包含全部 137 个界面与提示 key。机器拒绝码只映射静态文案，不显示原始错误或秘密。

| Key | 中文 | English |
|---|---|---|
|cancelRetry|重试上次连接清理|Retry previous connection cleanup|
|cancel_pending|正在处理上次连接，确认取消后才会开启新连接。您可以随时停止。|Finishing the previous connection. New access waits for confirmed cancellation; you can stop at any time.|
|cancel_storage_failed|无法保存连接取消记录，已阻止新连接。请检查配置目录是否可写。|Cannot save cancellation records. New access is blocked. Check configuration directory permissions.|
|cancel_not_supported|服务器暂不支持精确取消，已阻止新连接。请联系管理员升级服务。|The server does not support precise cancellation. New access is blocked. Ask an administrator to upgrade it.|
|cancel_auth_required|上次连接的取消凭证已失效，已阻止新连接。请登录对应账号刷新设备凭证。|Credentials for canceling the previous connection are invalid. New access is blocked. Sign in to its account to refresh device credentials.|
|cancel_rejected|服务器拒绝取消上次连接，已阻止新连接。请联系管理员。|The server refused to cancel the previous connection. New access is blocked. Contact an administrator.|
|acquire_cancelled|本次开启请求已取消，请主动再次开启。|This start request was canceled. Turn access on again when ready.|
|device_credential_required|设备访问凭证缺失，请重新登录。|Device access is missing. Sign in again.|
|plugin_secret_not_configured|服务器鉴权尚未配置，连接将自动重试。请联系管理员。|Server authorization is not configured. Retrying; contact an administrator.|
|grant_key_not_configured|服务器授权密钥尚未配置，连接将自动重试。请联系管理员。|Server authorization keys are not configured. Retrying; contact an administrator.|
|transport_not_configured|服务器连接尚未配置，连接将自动重试。请联系管理员。|Server transport is not configured. Retrying; contact an administrator.|
|product_access_denied|当前账号尚未获准使用 Remote，请联系管理员。|This account cannot use Remote yet. Contact an administrator.|
|invalid_harness_url|请输入不含凭证或参数的本机 Harness 根地址。|Enter the local Harness root address without credentials or parameters.|
|invalid_identifier|连接授权标识无效，请停止后重新开启；如持续出现，请联系客服。|Connection authorization ID is invalid. Stop and turn access on again; contact support if it persists.|
|invalid_sequence|连接授权版本无效，请停止后重新开启；如持续出现，请联系客服。|Connection authorization version is invalid. Stop and turn access on again; contact support if it persists.|
|invalid_operation|服务器鉴权协议不匹配，请联系客服。|Server authorization protocol does not match. Contact support.|
|subscriptionRefresh|刷新订阅信息|Refresh subscription|
|subscriptionPending|订阅信息待更新|Subscription information needs updating|
|confirming|正在确认|Confirming|
|connecting|正在连接|Connecting|
|connected|已连接|Connected|
|proxy_wait|正在接管|Taking over|
|reconnecting|正在重连|Reconnecting|
|stopped|已停止|Stopped|
|grant_superseded|已被接管|Taken over|
|subscription_expired|订阅到期|Subscription expired|
|grant_revoked|连接授权已释放，请主动开启以重新授权。|Connection authorization was released. Turn on access to authorize again.|
|device_revoked|设备身份已撤销，请重新登录。|Device identity was revoked. Sign in again.|
|device_credential_invalid|设备访问凭证已失效，请重新登录。|Device access expired. Sign in again.|
|protocol_upgrade_required|升级后请先重新登录 Penguin Go。旧授权不能用于新版连接。|Sign in to Penguin Go after upgrading. Previous authorization cannot be used.|
|subscription_required|请订阅 Remote 后再开启。|Subscribe to Remote before turning on access.|
|slot_over_quota|当前槽位超过订阅额度，请恢复额度后刷新。|This slot exceeds your subscription allowance. Restore the allowance and refresh.|
|grant_credential_invalid|连接授权校验失败，请主动重新开启。|Connection authorization failed. Turn access on again.|
|proxy_mismatch|服务器代理配置不匹配，请联系客服。|Server proxy configuration does not match. Contact support.|
|plugin_secret_invalid|服务器鉴权配置有误，请联系客服。|Server authorization is misconfigured. Contact support.|
|service_unavailable|暂时无法连接，正在自动重试。您可以随时停止。|Temporarily unavailable. Retrying automatically; you can stop at any time.|
|harness_unavailable|无法连接本地 Harness，请启动后重试。|Cannot connect to local Harness. Start it and retry.|
|save_failed|无法保存设置，请检查配置目录是否可写。|Cannot save settings. Check configuration directory permissions.|
|login_failed|登录未完成，请重新登录。|Sign-in did not complete. Try again.|
|grant_key_rotated|服务器授权密钥已更新，请主动重新开启。|Server authorization changed. Turn access on again.|
|request_id_conflict|开启请求发生冲突，请停止后重新开启。|Start request conflicts. Stop and turn access on again.|
|invalid_request|服务器返回的授权不完整，请联系客服。|The server returned incomplete authorization. Contact support.|
|checkingLogin|正在检查登录状态|Checking sign-in|
|checkingService|正在检查服务状态|Checking subscription|
|overview|远程访问概况|Remote access overview|
|supportGroup|客服微信群|Support|
|supportQrAlt|客服微信群二维码|Support WeChat group QR code|
|supportQrHint|微信扫码加入客服群|Scan with WeChat to join the support group|
|preferences|偏好设置|Preferences|
|theme|主题|Theme|
|language|语言|Language|
|systemTheme|跟随系统|System|
|lightTheme|浅色|Light|
|darkTheme|深色|Dark|
|localHarness|本机 Harness|Local Harness|
|remoteAccess|远程访问|Remote access|
|publicEntry|公网入口|Public access|
|publicAddress|公网地址|Public address|
|scanning|正在扫描|Scanning|
|checkingLocal|检查本机服务|Checking local service|
|waitingAuth|开启时验证本机连接|Local access is checked when starting|
|unassigned|未分配|Not assigned|
|assignedAfterLogin|开启后分配|Assigned when access is turned on|
|connection|连接|Connection|
|rescan|重新扫描|Scan again|
|login|登录 Penguin Go|Sign in to Penguin Go|
|relogin|重新登录|Sign in again|
|harnessConnection|Harness 远程连接|Harness remote connection|
|scanningLocal|正在扫描本机服务|Scanning for local Harness|
|switchStopped|已停止|Stopped|
|switchRunning|运行中|Running|
|switchConnecting|连接中|Connecting|
|switchReconnecting|重连中|Reconnecting|
|switchRecovering|恢复中|Restoring|
|unavailable|不可用|Unavailable|
|advanced|高级设置与诊断|Advanced settings and diagnostics|
|harnessAddress|Harness 地址|Harness address|
|harnessData|Harness 数据目录|Harness data directory|
|localAuth|本机认证|Local authentication|
|connectionStatus|连接状态|Connection status|
|subscriptionStatusTitle|订阅状态|Subscription|
|discoveryResult|发现结果|Discovery|
|authReady|本机连接已就绪|Local connection is ready|
|authError|无法连接本机 Harness|Cannot connect to local Harness|
|authUnchecked|开启时验证本机连接|Local access is checked when starting|
|runnerStarting|正在启动|Starting|
|runnerRunning|已开启|On|
|runnerStopping|正在停止|Stopping|
|runnerRecovering|正在恢复连接|Restoring connection|
|runnerError|连接遇到问题|Connection issue|
|runnerStopped|未开启|Off|
|runnerConnected|已连接|Connected|
|runnerConnecting|正在连接|Connecting|
|runnerReconnecting|正在重连|Reconnecting|
|loggedIn|已登录 Penguin Go|Signed in to Penguin Go|
|loggedInName|已登录 · {name}|Signed in · {name}|
|loggedOut|未登录 Penguin Go|Not signed in to Penguin Go|
|activeUntil|有效期至 {date}|Active until {date}|
|expiredOn|已于 {date} 到期|Expired on {date}|
|subscriptionExpired|订阅已到期|Subscription expired|
|subscriptionNone|Remote 服务尚未开通|Remote service is not enabled|
|subscriptionUnknown|订阅信息待更新|Subscription information needs updating|
|signInToCheckService|登录后查看服务状态|Sign in to check your subscription|
|renew|续约|Renew|
|subscribe|前往开通|Open subscription page|
|slotAndMax|当前设备 {slot} / 可同时连接 {max} 台|Device {slot} / {max} simultaneous connections|
|slotOnly|当前设备 {slot}|Device {slot}|
|maxOnly|可同时连接 {max} 台|Up to {max} simultaneous connections|
|harnessFound|已发现|Found|
|harnessNotFound|未发现|Not found|
|startHarness|请启动 Penguin Harness|Start Penguin Harness|
|kindDesktop|桌面端|Desktop|
|kindDevWeb|开发版前端|Development web|
|kindDevServer|开发版服务|Development server|
|kindCliWeb|Penguin Web|Penguin Web|
|noLocalHarness|未发现本机 Harness|Local Harness not found|
|noHarnessSummary|未发现本机 Penguin Harness|Local Penguin Harness not found|
|assigned|已分配|Assigned|
|deviceAfterLogin|登录后分配设备|Device assigned after sign-in|
|manualLocalPlaceholder|例如 http://localhost:7364|For example http://localhost:7364|
|manualDataPlaceholder|自动检查常见数据目录|Common data directories are checked automatically|
|recoveringNotice|连接暂时中断，正在等待网络并重连原授权；恢复成功后会自动重新开启。|Connection interrupted. Waiting for the network and reconnecting with the original authorization; access will turn back on automatically.|
|connectingNotice|正在连接公网服务器，连接成功后即可通过公网地址访问。|Connecting to the public server. The public address will work once connected.|
|reconnectingNotice|公网连接暂时中断，正在自动重连；无需重新登录。|The public connection was interrupted and is reconnecting automatically. You do not need to sign in again.|
|errorLeaseExpired|连接已中断。请重新开启远程访问；无需重新登录。|Connection stopped. Turn remote access on again; you do not need to sign in again.|
|errorTakenOver|这条远程连接已被其他设备接管。如需接管回来，请主动再次开启。|Remote access is now active on another device. Turn access on again to take over here.|
|errorSubscriptionExpired|Remote 服务已到期。续订后刷新订阅信息即可恢复资格。|Your Remote subscription has expired. Renew it, then refresh subscription information.|
|errorSubscriptionInactive|当前账号尚未开通 Remote 服务，或服务目前不可用。|Remote service is not enabled or is currently unavailable for this account.|
|errorSubscriptionUnknown|订阅信息待更新，请刷新订阅信息。|Subscription information needs updating. Refresh it.|
|errorServerOld|Penguin Go 暂不支持当前连接方式，请联系管理员更新服务。|Penguin Go does not support this connection method yet. Ask an administrator to update the service.|
|errorLeaseMissing|连接授权信息已失效，请重新登录 Penguin Go。|Connection authorization is no longer available. Sign in to Penguin Go again.|
|errorSave|无法保存连接状态，请检查本机配置目录是否可写。|Could not save the connection state. Check that the local configuration directory is writable.|
|errorLogin|登录未完成，请重新尝试登录 Penguin Go。|Sign-in was not completed. Try signing in to Penguin Go again.|
|errorHarness|无法连接本机 Harness，请确认它已启动后重试。|Cannot connect to local Harness. Start it and try again.|
|errorConfirm|暂时无法确认远程连接授权，请检查网络后重试。|Could not confirm remote access. Check your network and try again.|
|errorNetwork|连接暂时不可用，请检查网络后重试。|Connection is temporarily unavailable. Check your network and try again.|
|errorGeneric|操作未能完成，请稍后重试；如持续出现，请重新登录。|Could not complete this action. Try again shortly, or sign in again if it continues.|

## 登录回调页

| 场景 | 中文 | English |
|---|---|---|
| 标题 | 登录成功 | Signed in |
| 身份已保存 | 设备身份已更新。登录不会领取槽位，请返回 Penguin Remote 点击开启。 | Device identity updated. Sign-in does not take a slot. Return to Penguin Remote and turn access on. |
| 到期 | 设备身份已保存。订阅已到期，续订后刷新订阅信息，无需重新登录。 | Device identity saved. The subscription expired. Renew and refresh subscription information; no new sign-in is needed. |
| 尚未开通 | 设备身份已保存。请开通 Remote 后刷新订阅信息。 | Device identity saved. Subscribe to Remote and refresh subscription information. |
