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
import {
  ApiGatewayIcon,
  BadgeCheckIcon,
  CheckmarkCircle02Icon,
  CustomerService02Icon,
  Route02Icon,
  SecurityCheckIcon,
  Target02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { TemplateActions } from './template-actions'

type BusinessHomeProps = {
  isAuthenticated: boolean
  businessContactEmail?: string
  businessContactQrCode?: string
}

const ECOSYSTEMS = [
  'OpenAI',
  'Anthropic',
  'Google Gemini',
  'Microsoft Azure',
  'DeepSeek',
  'Alibaba Cloud',
  'Volcengine',
  'Baidu AI Cloud',
] as const

const LOOPING_ECOSYSTEMS = [
  ...ECOSYSTEMS.map((name) => ({
    key: `primary-${name}`,
    name,
    hidden: false,
  })),
  ...ECOSYSTEMS.map((name) => ({ key: `repeat-${name}`, name, hidden: true })),
]

function PartnerMarquee() {
  const { t } = useTranslation()

  return (
    <section className='px-6 py-20'>
      <div className='mx-auto max-w-6xl text-center'>
        <span className='border-primary/20 bg-primary/10 text-primary inline-flex rounded-full border px-4 py-1 text-xs font-semibold tracking-wide uppercase'>
          {t('Models and ecosystems')}
        </span>
        <h2 className='mt-4 text-2xl font-bold tracking-tight md:text-3xl'>
          {t('Connect to leading AI services through one platform')}
        </h2>
        <div className='business-marquee mt-9 overflow-hidden [mask-image:linear-gradient(to_right,transparent,black_12%,black_88%,transparent)] [-webkit-mask-image:linear-gradient(to_right,transparent,black_12%,black_88%,transparent)]'>
          <ul className='business-marquee-track flex w-max gap-5 py-2'>
            {LOOPING_ECOSYSTEMS.map((item) => (
              <li
                key={item.key}
                aria-hidden={item.hidden}
                className='border-border/70 bg-card/70 min-w-44 rounded-2xl border px-7 py-5 text-sm font-semibold tracking-wide shadow-sm backdrop-blur'
              >
                {item.name}
              </li>
            ))}
          </ul>
        </div>
        <p className='text-muted-foreground mt-5 text-sm'>
          {t('Aggregate dependable upstream services for stable model access.')}
        </p>
      </div>
    </section>
  )
}

export default function BusinessHome(props: BusinessHomeProps) {
  const { t } = useTranslation()

  return (
    <main className='overflow-hidden'>
      <section className='relative px-6 pt-28 pb-20 text-center md:pt-36 md:pb-24'>
        <div
          aria-hidden
          className='pointer-events-none absolute inset-0 -z-10 bg-[radial-gradient(ellipse_55%_45%_at_50%_18%,rgba(37,99,235,0.2),transparent_72%)] dark:opacity-80'
        />
        <div className='mx-auto max-w-5xl'>
          <span className='border-primary/20 bg-primary/10 text-primary inline-flex items-center gap-2 rounded-full border px-4 py-1.5 text-xs font-semibold tracking-wide uppercase'>
            <HugeiconsIcon
              icon={BadgeCheckIcon}
              className='size-4'
              strokeWidth={2}
            />
            {t('Official-grade access. No diluted routes.')}
          </span>
          <h1 className='from-foreground via-foreground mx-auto mt-7 max-w-4xl bg-gradient-to-r to-sky-500 bg-clip-text text-[clamp(2.6rem,6vw,4.4rem)] leading-[1.06] font-bold tracking-[-0.045em] text-transparent'>
            <span className='block'>{t('Official model access')}</span>
            <span className='block'>
              {t('Stable, undiluted quality with dependable support')}
            </span>
          </h1>
          <p className='text-muted-foreground mx-auto mt-6 max-w-2xl text-base leading-8 md:text-lg'>
            {t(
              'A high-quality API relay for teams that value official-grade model access, stable delivery, and accountable support.'
            )}
          </p>
          <div className='mt-8 flex justify-center'>
            <TemplateActions isAuthenticated={props.isAuthenticated} />
          </div>
          <div className='border-border/70 mx-auto mt-12 grid max-w-5xl gap-5 border-t pt-8 text-left md:grid-cols-3'>
            {[
              {
                icon: SecurityCheckIcon,
                title: t('Official upstream quality'),
                text: t('Clear sourcing and accountable delivery'),
              },
              {
                icon: Route02Icon,
                title: t('Transparent service'),
                text: t(
                  'Quality-first routing instead of opaque substitutions'
                ),
              },
              {
                icon: CustomerService02Icon,
                title: t('After-sales support'),
                text: t('Real people follow up when an issue occurs'),
              },
            ].map((item) => (
              <div key={item.title} className='flex gap-3'>
                <HugeiconsIcon
                  icon={item.icon}
                  className='mt-0.5 size-5 shrink-0 text-emerald-500'
                  strokeWidth={1.8}
                />
                <div>
                  <h2 className='text-sm font-semibold'>{item.title}</h2>
                  <p className='text-muted-foreground mt-1 text-xs leading-6'>
                    {item.text}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className='px-6 pb-20'>
        <div className='mx-auto max-w-6xl'>
          <div className='text-center'>
            <h2 className='text-3xl font-bold tracking-tight md:text-4xl'>
              {t('Quality before shortcuts')}
            </h2>
            <p className='text-muted-foreground mt-3'>
              {t(
                'Clear commitments, transparent billing, and accountable support'
              )}
            </p>
          </div>
          <div className='mt-10 grid gap-6 md:grid-cols-3'>
            {[
              {
                icon: Target02Icon,
                title: t('Quality-first routing'),
                text: t(
                  'Routing policies prioritize dependable delivery and make service boundaries explicit.'
                ),
              },
              {
                icon: ApiGatewayIcon,
                title: t('Claims customers can verify'),
                text: t(
                  'Pricing, model availability, and service rules stay visible instead of relying on vague promises.'
                ),
              },
              {
                icon: CustomerService02Icon,
                title: t('Support that closes the loop'),
                text: t(
                  'Issues are followed through with clear ownership, updates, and a practical resolution path.'
                ),
              },
            ].map((item) => (
              <article
                key={item.title}
                className='border-border/70 bg-card/60 rounded-3xl border p-8 shadow-sm transition-transform duration-200 hover:-translate-y-1 motion-reduce:transform-none'
              >
                <div className='bg-primary/10 text-primary flex size-11 items-center justify-center rounded-2xl'>
                  <HugeiconsIcon
                    icon={item.icon}
                    className='size-6'
                    strokeWidth={1.8}
                  />
                </div>
                <h3 className='mt-6 text-lg font-semibold'>{item.title}</h3>
                <p className='text-muted-foreground mt-3 text-sm leading-7'>
                  {item.text}
                </p>
              </article>
            ))}
          </div>
        </div>
      </section>

      <section className='border-border/70 bg-muted/25 border-y px-6 py-20'>
        <div className='mx-auto max-w-6xl rounded-[2rem]'>
          <div className='text-center'>
            <span className='text-primary text-xs font-semibold tracking-wide uppercase'>
              {t('From access to support')}
            </span>
            <h2 className='mt-3 text-3xl font-bold tracking-tight md:text-4xl'>
              {t('Every request has a clear service path.')}
            </h2>
          </div>
          <ol className='mt-10 grid gap-8 md:grid-cols-3'>
            {[
              [
                '01',
                t('Connect through one compatible API'),
                t(
                  'Keep the integration surface consistent while the platform manages upstream access.'
                ),
              ],
              [
                '02',
                t('Route with quality as the priority'),
                t(
                  'Availability checks and routing policies select a suitable service path.'
                ),
              ],
              [
                '03',
                t('Escalate with real support'),
                t(
                  'When an issue needs attention, the support trail remains clear and accountable.'
                ),
              ],
            ].map(([number, title, text]) => (
              <li key={number} className='text-center'>
                <span className='mx-auto flex size-11 items-center justify-center rounded-full bg-gradient-to-br from-blue-600 to-cyan-500 font-mono text-sm font-bold text-white shadow-lg shadow-blue-500/20'>
                  {number}
                </span>
                <h3 className='mt-5 font-semibold'>{title}</h3>
                <p className='text-muted-foreground mt-2 text-sm leading-7'>
                  {text}
                </p>
              </li>
            ))}
          </ol>
        </div>
      </section>

      <PartnerMarquee />

      <section className='border-border/70 border-t px-6 py-20 text-center'>
        <div className='mx-auto max-w-3xl'>
          <HugeiconsIcon
            icon={CheckmarkCircle02Icon}
            className='mx-auto size-9 text-emerald-500'
            strokeWidth={1.8}
          />
          <h2 className='mt-5 text-3xl font-bold tracking-tight md:text-4xl'>
            {t('Choose quality you can stand behind.')}
          </h2>
          <p className='text-muted-foreground mt-4 leading-7'>
            {t(
              'Start with a unified API, transparent customer pricing, and support that stays accountable.'
            )}
          </p>
          <div className='mt-8 flex justify-center'>
            <TemplateActions isAuthenticated={props.isAuthenticated} />
          </div>
        </div>
      </section>

    </main>
  )
}
