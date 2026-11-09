# Pinned sing-vmess source

Upstream: https://github.com/hiddify/sing-vmess
Revision: 93e7499b6da443ad5016f4c4ce469145af311832
License: GPLv3 or later (see retained LICENSE).
Native Vision protection port: 3x-ui/deploy/hiddify/sing-vmess-protection.patch
applied once to the committed source. No release-time overlay.
Reverse framing is not yet included.

Native reverse framing port: 3x-ui/deploy/hiddify/sing-vmess-reverse.patch
Three wire-protocol files applied once. The Vision transport's older
patch hunks remain blocked pending separate source-level review.
No release-time patching of this third-party module.
