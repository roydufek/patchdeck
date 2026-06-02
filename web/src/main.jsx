import React from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './styles.css'
import App from './App.jsx'

// Single app-wide query client. Defaults tuned for a live ops dashboard:
// keep showing last-good data while refetching (no flip-to-empty flicker),
// and don't hammer the server on every tab focus.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
      staleTime: 10_000,
    },
  },
})

createRoot(document.getElementById('root')).render(
  <QueryClientProvider client={queryClient}>
    <App />
  </QueryClientProvider>
)
