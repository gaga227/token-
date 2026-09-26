/*
Copyright (C) 2023-2026 QuantumNous

AGPL-3.0，同项目其余文件许可一致。
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

import { DataReports } from '@/features/data-reports'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/data-reports/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()

    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({
        to: '/403',
      })
    }
  },
  component: DataReports,
})
