// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

import {createApp} from 'vue'
import App from './App.vue'
import {i18n, initI18n} from './i18n'
import {initTheme} from './composables/useTheme'
import './assets/theme.css'
import './assets/migrated.css'
import './assets/style.css'
import './components/auth/auth.css'
import './style.css'

// Тема применяется до монтирования: иначе на долю секунды мелькнёт тёмная
initTheme()

// Язык выбирается до монтирования: иначе на долю секунды мелькнёт английский интерфейс
initI18n().finally(() => {
  createApp(App).use(i18n).mount('#app')
})
