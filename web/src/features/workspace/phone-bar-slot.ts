import { createContext, useContext } from "react";

/** On phones, the center of the top bar, where a page may put its own controls. Null at other sizes. */
export const PhoneBarSlot = createContext<HTMLElement | null>(null);

export const usePhoneBarSlot = () => useContext(PhoneBarSlot);
