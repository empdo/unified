import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource/archivo-black/400.css'
import '@fontsource/archivo-narrow/600.css'
import '@fontsource/archivo-narrow/700.css'
import '@fontsource-variable/source-serif-4/index.css'
import App from './App'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
