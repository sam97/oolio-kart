import { create } from "zustand";
import axios from "axios";
import { persist, createJSONStorage } from "zustand/middleware";
import { toast } from "react-toastify";

export interface IProduct {
  id: string;
  image: any;
  name: string;
  desc: string;
  price: number;
}

export interface ICart {
  id: string;
  name: string;
  price: number;
  qty: number;
}

interface CartState {
  products: IProduct[];
  carts: { [key: string]: ICart }; // Object with productId as keys
  addToCart: (product: IProduct) => void;
  openWebModal: boolean;
  setOpenWebModal: (open: boolean) => void;
  openMobileModal: boolean;
  setOpenMobileModal: (open: boolean) => void;
  getTotalAmount: () => number;
  incrementQty: (id: string) => void;
  decrementQty: (id: string) => void;
  getStorageCart: () => void;
}

const handleRequest = async (
  request: () => Promise<any>,
  successMessage?: string,
  errorMessage?: string
) => {
  try {
    const response = await request();
    successMessage && toast.success(successMessage);
    return response;
  } catch (error: any) {
    throw error;
  }
};

export const createCartStore = create<CartState>()(
  persist(
    (set, get) => ({
      products: [
        {
          id: "1",
          image: "/assets/dessert.png",
          name: "Waffle 1",
          desc: "Waffle with Berries",
          price: 10,
        },
        {
          id: "2",
          image: "/assets/dessert.png",
          name: "Waffle 2",
          desc: "Waffle with Berries",
          price: 100,
        },
        {
          id: "3",
          image: "/assets/dessert.png",
          name: "Waffle 3",
          desc: "Waffle with Berries",
          price: 50,
        },
      ],
      carts: {},
      openWebModal: true,
      setOpenWebModal: (open: boolean) => {
        set({ openWebModal: open });
      },
      openMobileModal: false,
      setOpenMobileModal: (open: boolean) => {
        set({ openMobileModal: open });
      },
      addToCart: (product: IProduct) => {
        const { carts } = get();

        if (carts[product.id]) return;

        carts[product.id] = {
          id: product.id,
          name: product.name,
          price: product.price,
          qty: 1,
        };

        set({ carts: { ...carts } });
      },
      getTotalAmount: () => {
        const { carts } = get();

        const total = Object.values(carts).reduce((total, cart) => {
          return total + cart.price * cart.qty;
        }, 0);

        return parseFloat(total.toFixed(2));
      },

      incrementQty: (id: string) => {
        const { carts } = get();
        const cartItem = carts[id];

        if (cartItem) {
          set({
            carts: {
              ...carts,
              [id]: {
                ...cartItem,
                qty: cartItem.qty + 1,
              },
            },
          });
        }
      },

      decrementQty: (id: string) => {
        const { carts } = get();
        const cartItem = carts[id];
        if (cartItem && cartItem.qty > 1) {
          set({
            carts: {
              ...carts,
              [id]: {
                ...cartItem,
                qty: cartItem.qty - 1,
              },
            },
          });
        } else if (cartItem && cartItem.qty === 1) {
          const updatedCarts = { ...carts };
          delete updatedCarts[id]; // Remove item if qty reaches 0
          set({ carts: updatedCarts });
        }
      },
      getStorageCart: () => {
        const storedCart: any = localStorage.getItem("cart-storage");
        if (storedCart) {
          const parsedCart = JSON.parse(storedCart);
          set({ carts: { ...parsedCart } });
        }
      },
    }),
    {
      name: "cart-storage",
      storage: createJSONStorage(() => localStorage),
      partialize: (state) => ({ carts: state.carts }),
    }
  )
);
