import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { AuthLayout } from './auth-layout'
import { pathForStage } from './guards'
import { applyMe } from './me'

export function LoginPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const login = useMutation({
    mutationFn: () => unwrap(api.POST('/auth/login', { body: { username: username.trim(), password } })),
    onSuccess: async (me) => {
      // Cache first, then navigate – the target route's guard reads this Me.
      applyMe(queryClient, me)
      await navigate({ to: pathForStage(me.stage) })
    },
  })

  return (
    <AuthLayout title={t('login.title')} description={t('login.description')}>
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          login.mutate()
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="login-username">{t('login.username')}</Label>
          <Input
            id="login-username"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={64}
            autoFocus
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="login-password">{t('login.password')}</Label>
          <Input
            id="login-password"
            type="password"
            autoComplete="current-password"
            maxLength={256}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {login.error ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(login.error, t)}
          </p>
        ) : null}
        <Button type="submit" className="w-full" disabled={login.isPending || !username.trim() || !password}>
          {login.isPending ? t('login.submitting') : t('login.submit')}
        </Button>
      </form>
    </AuthLayout>
  )
}
