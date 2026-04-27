import { createContext, useContext } from 'react';

interface BackdropContextValue {
  setBackdrop: (url: string | null) => void;
}

export const BackdropContext = createContext<BackdropContextValue>({ setBackdrop: () => {} });
export const useBackdrop = () => useContext(BackdropContext);
