/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'

import type { Asset } from '../types'
import { getAssetStatusVariant } from './asset-columns'

type AssetStatusBadgeProps = {
  asset: Asset
  className?: string
}

export function AssetStatusBadge({ asset, className }: AssetStatusBadgeProps) {
  const { t } = useTranslation()
  const variant = getAssetStatusVariant(asset.Status)
  const label = t(asset.Status || 'Unknown')

  if (asset.Status === 'Failed' && asset.Error?.Message) {
    return (
      <Popover>
        <PopoverTrigger
          className={`inline-flex shrink-0 rounded-full focus-visible:ring-2 focus-visible:outline-none ${className ?? ''}`}
        >
          <StatusBadge label={label} variant={variant} copyable={false} />
        </PopoverTrigger>
        <PopoverContent align='start' className='w-80'>
          <div className='space-y-1.5'>
            <p className='text-destructive font-medium'>
              {asset.Error.Code
                ? `${asset.Error.Code}`
                : t('Failure reason')}
            </p>
            <p className='text-muted-foreground break-words text-xs'>
              {asset.Error.Message}
            </p>
          </div>
        </PopoverContent>
      </Popover>
    )
  }

  return (
    <StatusBadge
      label={label}
      variant={variant}
      copyable={false}
      className={className}
    />
  )
}
