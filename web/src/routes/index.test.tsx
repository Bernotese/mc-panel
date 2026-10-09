import { render, screen } from '@testing-library/react'
import { Home } from './index'

test('renders app name', () => {
  render(<Home />)
  expect(screen.getByText('mc-panel')).toBeInTheDocument()
})
