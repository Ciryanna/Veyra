import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './index.css'

// 主题由 index.html 的 class="dark" 默认，App 里挂切换与记忆。
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
