# 素材库文件存储配置

素材库上传文件支持两种存储后端，通过环境变量切换：

- `local`（默认）：文件存储在网关本地磁盘 `data/asset-library`，通过 `/api/asset/files/*` 公开路由提供下载
- `oss`：文件存储在阿里云 OSS，通过签名 URL（私有 bucket）或公共 URL（公共读 bucket / CDN 域名）提供下载

## 环境变量

### 通用

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ASSET_STORAGE_BACKEND` | `local` | 存储后端：`local` 或 `oss` |
| `ASSET_UPLOAD_MAX_MB` | `100` | 单个上传文件大小上限（MB） |
| `ASSET_STORAGE_DIR` | `data/asset-library` | 本地后端存储目录 |

### OSS 后端（`ASSET_STORAGE_BACKEND=oss` 时必填前四项）

| 变量 | 示例 | 说明 |
|------|------|------|
| `ASSET_OSS_REGION` | `oss-cn-beijing` | OSS 区域 ID（也接受 `cn-beijing` 或完整 endpoint） |
| `ASSET_OSS_BUCKET` | `my-bucket` | Bucket 名称 |
| `ASSET_OSS_ACCESS_KEY_ID` | `LTAI...` | RAM 子账号 AccessKey ID（建议只授予该 bucket 的读写权限） |
| `ASSET_OSS_ACCESS_KEY_SECRET` | `...` | RAM 子账号 AccessKey Secret |
| `ASSET_OSS_KEY_PREFIX` | `asset-library/` | 对象 key 前缀（可选） |
| `ASSET_OSS_URL_EXPIRY_SECONDS` | `3600` | 签名 URL 有效期（可选） |
| `ASSET_OSS_PUBLIC_BASE_URL` | `https://cdn.example.com` | 公共读 bucket / CDN 域名；配置后返回普通 URL 而非签名 URL（可选） |
| `ASSET_OSS_ENDPOINT` | `https://oss-cn-beijing-internal.aliyuncs.com` | 覆盖 endpoint，例如服务器在同区域时走内网 endpoint（可选） |

## 签名 URL 与复制链路

私有 bucket 的签名 URL 会过期，因此：

- 素材记录中的 `SourceURL` 仅作为兜底；实际复制到上游渠道（`ReplicateAsset`）和前端展示时会根据 `StorageKey`（形如 `oss://<bucket>/<objectKey>`）重新生成新鲜签名 URL
- 本地后端的 URL 永不过期，直接使用 `SourceURL`

## 端到端验证

配置真实凭据后可运行针对线上 bucket 的回环测试：

```bash
ASSET_OSS_LIVE_TEST=1 \
ASSET_STORAGE_BACKEND=oss \
ASSET_OSS_REGION=oss-cn-beijing \
ASSET_OSS_BUCKET=<bucket> \
ASSET_OSS_ACCESS_KEY_ID=<ak> \
ASSET_OSS_ACCESS_KEY_SECRET=<sk> \
go test ./common/ -run TestAssetOSSLiveRoundTrip -v
```

测试内容：上传 PNG → 签名 URL 下载并比对字节 → 删除对象 → 重复删除（幂等）。

## RAM 最小权限策略

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["oss:GetObject", "oss:PutObject", "oss:DeleteObject"],
      "Resource": "acs:oss:*:*:<bucket-name>/*"
    }
  ]
}
```
